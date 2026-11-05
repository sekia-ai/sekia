package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	gh "github.com/google/go-github/v68/github"
	"github.com/rs/zerolog"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

// RepoRef is a parsed owner/repo reference.
type RepoRef struct {
	Owner string
	Repo  string
}

// String returns "owner/repo".
func (r RepoRef) String() string { return r.Owner + "/" + r.Repo }

const (
	// pageSize is the GitHub API page size used for every list call.
	pageSize = 100

	// tickTimeout bounds a single tick, including a full sweep.
	tickTimeout = 10 * time.Minute

	// maxBackoff caps the exponential backoff applied after consecutive errors.
	maxBackoff = 15 * time.Minute

	// sinceSlack is subtracted from the watermark when asking GitHub for
	// updated items so that second-granularity timestamps never hide an item.
	sinceSlack = time.Second

	// startupLookback is how far back a poller that never sweeps looks on its
	// very first run, when there is no saved state to resume from.
	startupLookback = time.Hour
)

// Poller periodically queries the GitHub REST API and publishes events.
//
// Modes:
//   - time mode (default): issues, PRs and comments updated since the last
//     tick are emitted as github.issue.*, github.pr.* and github.comment.created.
//   - label mode (labels set, match_prs false): issues carrying ANY of the
//     configured labels are emitted as github.issue.matched.
//   - PR-match mode (match_prs true): PRs in the configured state (optionally
//     carrying ALL configured labels) are emitted as github.pr.matched.
//
// Every tick fetches only items updated since the previous successful tick,
// 100 per page and sorted by updated time, so a steady-state tick costs one
// API call per repo and label. In the matched modes the first tick without
// saved state is a full sweep of every matching item, and a sweep is repeated
// every sweep interval, so an item whose event was lost while the daemon or an
// agent was down is emitted again. Items whose updated time has not changed
// since their last emission are suppressed between sweeps.
//
// The watermarks are saved to a state file after every successful tick and
// restored at start, so a restart resumes where the previous run stopped. With
// a sweep interval of zero the poller never sweeps: it resumes from the saved
// state, or starts one hour back when there is none.
//
// Errors leave the watermark untouched and back off exponentially; GitHub
// rate limit responses pause polling until the reset time GitHub reports.
type Poller struct {
	client  GitHubClient
	onEvent func(protocol.Event)
	logger  zerolog.Logger
	now     func() time.Time

	mu       sync.Mutex // guards settings, which config reload may replace
	settings pollSettings
	// intervalCh tells Run to reset its ticker after a reload changed the interval.
	intervalCh chan time.Duration

	// Matched modes: high-water mark of updated_at over all emitted items.
	// Zero means the next tick is a sweep.
	watermark time.Time
	lastSweep time.Time
	// seen maps "owner/repo#number" to the updated_at of its last emission.
	seen map[string]time.Time

	// Time mode: wall-clock watermark used for the `since` parameter.
	lastSync time.Time

	stateFile    string // "" disables persistence
	backoffUntil time.Time
	failures     int
}

// pollState is the on-disk form of the poller's watermarks.
type pollState struct {
	Watermark time.Time            `json:"watermark"`
	LastSweep time.Time            `json:"last_sweep"`
	LastSync  time.Time            `json:"last_sync"`
	SavedAt   time.Time            `json:"saved_at"`
	Seen      map[string]time.Time `json:"seen,omitempty"` // dedup entries near the watermark (pruned, small)
}

type pollSettings struct {
	interval      time.Duration
	sweepInterval time.Duration
	repos         []RepoRef
	labels        []string
	state         string
	matchPRs      bool
}

func (s pollSettings) labelMode() bool   { return len(s.labels) > 0 && !s.matchPRs }
func (s pollSettings) prMatchMode() bool { return s.matchPRs }
func (s pollSettings) matchedMode() bool { return s.labelMode() || s.prMatchMode() }

// PollerConfig holds parameters for creating a Poller.
type PollerConfig struct {
	Client        GitHubClient
	Interval      time.Duration
	SweepInterval time.Duration // 0 = never sweep; resume from StateFile or start one hour back
	StateFile     string        // watermark persistence; "" disables it
	Repos         []RepoRef
	Labels        []string
	State         string
	MatchPRs      bool
	OnEvent       func(protocol.Event)
	Logger        zerolog.Logger
}

// NewPoller creates a GitHub API poller.
func NewPoller(cfg PollerConfig) *Poller {
	p := &Poller{
		client:  cfg.Client,
		onEvent: cfg.OnEvent,
		logger:  cfg.Logger.With().Str("component", "poller").Logger(),
		now:     time.Now,
		settings: pollSettings{
			interval:      cfg.Interval,
			sweepInterval: cfg.SweepInterval,
			repos:         cfg.Repos,
			labels:        cfg.Labels,
			state:         cfg.State,
			matchPRs:      cfg.MatchPRs,
		},
		intervalCh: make(chan time.Duration, 1),
		seen:       make(map[string]time.Time),
		lastSync:   time.Now().Add(-5 * time.Minute),
		stateFile:  cfg.StateFile,
	}
	p.restoreState()
	return p
}

// restoreState loads the watermarks saved by a previous run, if any.
func (p *Poller) restoreState() {
	if p.stateFile == "" {
		return
	}
	data, err := os.ReadFile(p.stateFile)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			p.logger.Warn().Err(err).Str("file", p.stateFile).Msg("cannot read poll state; starting fresh")
		}
		return
	}
	var st pollState
	if err := json.Unmarshal(data, &st); err != nil {
		p.logger.Warn().Err(err).Str("file", p.stateFile).Msg("cannot parse poll state; starting fresh")
		return
	}
	p.watermark = st.Watermark
	p.lastSweep = st.LastSweep
	if !st.LastSync.IsZero() {
		p.lastSync = st.LastSync
	}
	if len(st.Seen) > 0 {
		p.seen = st.Seen
	}
	p.logger.Info().
		Str("file", p.stateFile).
		Time("watermark", st.Watermark).
		Time("saved_at", st.SavedAt).
		Msg("restored poll state")
}

// saveState writes the watermarks atomically. Failures are logged, not fatal:
// the worst case is a sweep or a one-hour lookback on the next start.
// Caller holds p.mu.
func (p *Poller) saveState() {
	if p.stateFile == "" {
		return
	}
	st := pollState{Watermark: p.watermark, LastSweep: p.lastSweep, LastSync: p.lastSync, SavedAt: p.now(), Seen: p.seen}
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p.stateFile), 0o700); err != nil {
		p.logger.Warn().Err(err).Str("file", p.stateFile).Msg("cannot create poll state directory")
		return
	}
	tmp := p.stateFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		p.logger.Warn().Err(err).Str("file", tmp).Msg("cannot write poll state")
		return
	}
	if err := os.Rename(tmp, p.stateFile); err != nil {
		p.logger.Warn().Err(err).Str("file", p.stateFile).Msg("cannot save poll state")
	}
}

// Run starts the polling loop. Blocks until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) error {
	p.tick(ctx)

	ticker := time.NewTicker(p.currentSettings().interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case d := <-p.intervalCh:
			ticker.Reset(d)
		case <-ticker.C:
			p.tick(ctx)
		}
	}
}

// Update replaces the poller settings. Interval changes take effect on the
// next tick. A change of repos, labels, state or mode discards the watermark:
// the next tick sweeps, or starts one hour back when sweeps are disabled.
func (p *Poller) Update(cfg PollerConfig) {
	next := pollSettings{
		interval:      cfg.Interval,
		sweepInterval: cfg.SweepInterval,
		repos:         cfg.Repos,
		labels:        cfg.Labels,
		state:         cfg.State,
		matchPRs:      cfg.MatchPRs,
	}

	p.mu.Lock()
	prev := p.settings
	p.settings = next
	if prev.interval != next.interval && next.interval > 0 {
		select {
		case p.intervalCh <- next.interval:
		default:
		}
	}
	if !sameRepos(prev.repos, next.repos) || !sameStrings(prev.labels, next.labels) ||
		prev.state != next.state || prev.matchPRs != next.matchPRs {
		p.watermark = time.Time{}
		p.lastSweep = time.Time{}
		p.seen = make(map[string]time.Time)
	}
	p.mu.Unlock()

	p.logger.Info().
		Dur("interval", next.interval).
		Dur("sweep_interval", next.sweepInterval).
		Int("repos", len(next.repos)).
		Strs("labels", next.labels).
		Bool("match_prs", next.matchPRs).
		Msg("poller settings updated")
}

func (p *Poller) currentSettings() pollSettings {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.settings
}

// tick runs one polling pass over every repo.
func (p *Poller) tick(parent context.Context) {
	s := p.currentSettings()
	now := p.now()

	if now.Before(p.backoffUntil) {
		p.logger.Debug().Time("until", p.backoffUntil).Msg("poll backing off")
		return
	}

	ctx, cancel := context.WithTimeout(parent, tickTimeout)
	defer cancel()

	if s.matchedMode() {
		p.tickMatched(ctx, s, now)
		return
	}
	p.tickTime(ctx, s, now)
}

// tickMatched handles label mode and PR-match mode.
func (p *Poller) tickMatched(ctx context.Context, s pollSettings, now time.Time) {
	p.mu.Lock()
	if p.watermark.IsZero() && s.sweepInterval == 0 {
		// Never sweep: without saved state, start one hour back.
		p.watermark = now.Add(-startupLookback)
	}
	sweep := p.watermark.IsZero() ||
		(s.sweepInterval > 0 && (p.lastSweep.IsZero() || now.Sub(p.lastSweep) >= s.sweepInterval))
	since := p.watermark
	p.mu.Unlock()
	if sweep {
		since = time.Time{}
	}

	seenThisTick := make(map[string]bool)
	maxUpdated := since
	var emitted, fetched int

	for _, repo := range s.repos {
		var n, f int
		var mx time.Time
		var err error
		if s.prMatchMode() {
			n, f, mx, err = p.pollPRs(ctx, s, repo, since, sweep, seenThisTick)
		} else {
			n, f, mx, err = p.pollLabels(ctx, s, repo, since, sweep, seenThisTick)
		}
		emitted += n
		fetched += f
		if mx.After(maxUpdated) {
			maxUpdated = mx
		}
		if err != nil {
			p.fail(err, repo, s.interval)
			return
		}
	}

	p.failures = 0
	p.mu.Lock()
	if sweep {
		p.lastSweep = now
	}
	if !maxUpdated.IsZero() {
		p.watermark = maxUpdated
	}
	p.pruneSeen()
	p.saveState()
	p.mu.Unlock()

	p.logger.Debug().
		Bool("sweep", sweep).
		Int("fetched", fetched).
		Int("emitted", emitted).
		Time("watermark", p.watermark).
		Msg("poll tick complete")
}

// pollLabels emits github.issue.matched for issues carrying any configured label.
func (p *Poller) pollLabels(ctx context.Context, s pollSettings, repo RepoRef, since time.Time, sweep bool, seenThisTick map[string]bool) (emitted, fetched int, maxUpdated time.Time, err error) {
	apiSince := apiSinceFor(since)
	for _, label := range s.labels {
		page := 1
		for page > 0 {
			issues, next, lerr := p.client.ListIssuesByLabelPage(ctx, repo.Owner, repo.Repo, []string{label}, s.state, apiSince, page, pageSize)
			if lerr != nil {
				return emitted, fetched, maxUpdated, lerr
			}
			fetched += len(issues)
			stop := false
			for _, issue := range issues {
				if issue.PullRequestLinks != nil {
					continue
				}
				updated := issue.GetUpdatedAt().Time
				if !since.IsZero() && updated.Before(since) {
					stop = true // sorted by updated desc: everything after this is older
					break
				}
				if updated.After(maxUpdated) {
					maxUpdated = updated
				}
				key := fmt.Sprintf("%s#%d", repo, issue.GetNumber())
				if !p.shouldEmit(key, updated, sweep, seenThisTick) {
					continue
				}
				ev := MapLabelMatchedIssue(issue, repo.Owner, repo.Repo)
				ev.Payload["sweep"] = sweep
				p.onEvent(ev)
				emitted++
				p.logger.Debug().Str("type", ev.Type).Int("number", issue.GetNumber()).Str("label", label).Bool("sweep", sweep).Msg("label-matched issue")
			}
			if stop {
				break
			}
			page = next
		}
	}
	return emitted, fetched, maxUpdated, nil
}

// pollPRs emits github.pr.matched for PRs in the configured state.
func (p *Poller) pollPRs(ctx context.Context, s pollSettings, repo RepoRef, since time.Time, sweep bool, seenThisTick map[string]bool) (emitted, fetched int, maxUpdated time.Time, err error) {
	page := 1
	for page > 0 {
		prs, next, lerr := p.client.ListPRsByStatePage(ctx, repo.Owner, repo.Repo, s.state, s.labels, page, pageSize)
		if lerr != nil {
			return emitted, fetched, maxUpdated, lerr
		}
		fetched += len(prs)
		stop := false
		for _, pr := range prs {
			updated := pr.GetUpdatedAt().Time
			if !since.IsZero() && updated.Before(since) {
				stop = true // sorted by updated desc
				break
			}
			if updated.After(maxUpdated) {
				maxUpdated = updated
			}
			key := fmt.Sprintf("%s#%d", repo, pr.GetNumber())
			if !p.shouldEmit(key, updated, sweep, seenThisTick) {
				continue
			}
			ev := MapPRMatched(pr, repo.Owner, repo.Repo)
			ev.Payload["sweep"] = sweep
			p.onEvent(ev)
			emitted++
			p.logger.Debug().Str("type", ev.Type).Int("number", pr.GetNumber()).Bool("sweep", sweep).Msg("PR-matched event")
		}
		if stop {
			break
		}
		page = next
	}
	return emitted, fetched, maxUpdated, nil
}

// shouldEmit records the item and reports whether it must be emitted: always
// during a sweep, otherwise only when its updated time changed since the last
// emission. An item returned twice in one tick (two labels) is emitted once.
func (p *Poller) shouldEmit(key string, updated time.Time, sweep bool, seenThisTick map[string]bool) bool {
	if seenThisTick[key] {
		return false
	}
	seenThisTick[key] = true

	p.mu.Lock()
	defer p.mu.Unlock()
	if !sweep {
		if last, ok := p.seen[key]; ok && last.Equal(updated) {
			return false
		}
	}
	p.seen[key] = updated
	return true
}

// pruneSeen drops dedup entries that can no longer be returned by an
// incremental query. Caller holds p.mu.
func (p *Poller) pruneSeen() {
	if p.watermark.IsZero() {
		return
	}
	cutoff := p.watermark.Add(-time.Minute)
	for key, updated := range p.seen {
		if updated.Before(cutoff) {
			delete(p.seen, key)
		}
	}
}

// tickTime handles the default time-based mode.
func (p *Poller) tickTime(ctx context.Context, s pollSettings, now time.Time) {
	since := p.lastSync
	var emitted int
	for _, repo := range s.repos {
		n, err := p.pollRepoSince(ctx, repo, since)
		emitted += n
		if err != nil {
			p.fail(err, repo, s.interval)
			return
		}
	}
	p.failures = 0
	p.lastSync = now
	p.mu.Lock()
	p.saveState()
	p.mu.Unlock()
	p.logger.Debug().Int("emitted", emitted).Time("since", since).Msg("poll tick complete")
}

func (p *Poller) pollRepoSince(ctx context.Context, repo RepoRef, since time.Time) (int, error) {
	var emitted int

	for page := 1; page > 0; {
		issues, next, err := p.client.ListIssuesPage(ctx, repo.Owner, repo.Repo, since, page, pageSize)
		if err != nil {
			return emitted, err
		}
		for _, issue := range issues {
			if issue.PullRequestLinks != nil {
				continue
			}
			ev := MapPolledIssue(issue, repo.Owner, repo.Repo, since)
			p.onEvent(ev)
			emitted++
			p.logger.Debug().Str("type", ev.Type).Int("number", issue.GetNumber()).Msg("issue event")
		}
		page = next
	}

	for page := 1; page > 0; {
		prs, next, err := p.client.ListPRsPage(ctx, repo.Owner, repo.Repo, since, page, pageSize)
		if err != nil {
			return emitted, err
		}
		for _, pr := range prs {
			ev := MapPolledPR(pr, repo.Owner, repo.Repo, since)
			p.onEvent(ev)
			emitted++
			p.logger.Debug().Str("type", ev.Type).Int("number", pr.GetNumber()).Msg("PR event")
		}
		page = next
	}

	for page := 1; page > 0; {
		comments, next, err := p.client.ListCommentsPage(ctx, repo.Owner, repo.Repo, since, page, pageSize)
		if err != nil {
			return emitted, err
		}
		for _, comment := range comments {
			p.onEvent(MapPolledComment(comment, repo.Owner, repo.Repo))
			emitted++
		}
		page = next
	}

	return emitted, nil
}

// fail logs a poll error and schedules the next attempt. Rate limit errors
// wait for GitHub's reset time; other errors back off exponentially.
func (p *Poller) fail(err error, repo RepoRef, interval time.Duration) {
	now := p.now()
	var wait time.Duration

	var rateErr *gh.RateLimitError
	var abuseErr *gh.AbuseRateLimitError
	switch {
	case errors.As(err, &rateErr):
		wait = rateErr.Rate.Reset.Time.Sub(now) + time.Second
	case errors.As(err, &abuseErr):
		wait = time.Minute
		if abuseErr.RetryAfter != nil {
			wait = *abuseErr.RetryAfter
		}
	default:
		p.failures++
		wait = interval << min(p.failures, 8)
	}
	if wait < interval {
		wait = interval
	}
	if wait > maxBackoff {
		wait = maxBackoff
	}
	p.backoffUntil = now.Add(wait)

	p.logger.Warn().Err(err).
		Str("repo", repo.String()).
		Dur("retry_in", wait).
		Msg("poll failed")
}

// apiSinceFor returns the value passed to GitHub's `since` parameter for a
// watermark: zero stays zero (no filter), otherwise one second of slack.
func apiSinceFor(since time.Time) time.Time {
	if since.IsZero() {
		return since
	}
	return since.Add(-sinceSlack)
}

func sameRepos(a, b []RepoRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ParseRepos parses a slice of "owner/repo" strings into RepoRef values.
func ParseRepos(repos []string) ([]RepoRef, error) {
	result := make([]RepoRef, 0, len(repos))
	for _, r := range repos {
		parts := strings.SplitN(r, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid repo format %q, expected owner/repo", r)
		}
		result = append(result, RepoRef{Owner: parts[0], Repo: parts[1]})
	}
	return result, nil
}
