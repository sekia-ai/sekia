package github

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

// RepoRef is a parsed owner/repo reference.
type RepoRef struct {
	Owner string
	Repo  string
}

type pollPhase int

const (
	phaseIssues   pollPhase = 0
	phasePRs      pollPhase = 1
	phaseComments pollPhase = 2
	phaseCount    pollPhase = 3
)

// pollCursor tracks position within a polling cycle.
type pollCursor struct {
	repoIdx int
	phase   pollPhase
	page    int // GitHub API page number (1-based)
}

// Poller periodically queries the GitHub REST API for updated issues, PRs, and
// comments. Each tick fetches at most perTick items, resuming from where it
// left off via a cursor. When all items for all repos are consumed, it advances
// lastSyncTime and starts a new cycle.
type Poller struct {
	client   GitHubClient
	interval time.Duration
	perTick  int
	repos    []RepoRef
	onEvent  func(protocol.Event)
	logger   zerolog.Logger

	lastSyncTime time.Time
	cursor       pollCursor
	cycleSince   time.Time
	inCycle      bool
}

// NewPoller creates a GitHub API poller.
func NewPoller(client GitHubClient, interval time.Duration, perTick int, repos []RepoRef, onEvent func(protocol.Event), logger zerolog.Logger) *Poller {
	return &Poller{
		client:       client,
		interval:     interval,
		perTick:      perTick,
		repos:        repos,
		onEvent:      onEvent,
		logger:       logger.With().Str("component", "poller").Logger(),
		lastSyncTime: time.Now().Add(-5 * time.Minute),
	}
}

// Run starts the polling loop. Blocks until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) error {
	p.tick(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			p.tick(ctx)
		}
	}
}

func (p *Poller) tick(ctx context.Context) {
	if !p.inCycle {
		p.cursor = pollCursor{repoIdx: 0, phase: phaseIssues, page: 1}
		p.cycleSince = time.Now()
		p.inCycle = true
	}

	budget := p.perTick
	var failed bool

	for budget > 0 && !p.cursorExhausted() {
		repo := p.repos[p.cursor.repoIdx]
		n, err := p.fetchAndEmit(ctx, repo, p.lastSyncTime, budget)
		if err != nil {
			p.logger.Error().Err(err).
				Str("repo", repo.Owner+"/"+repo.Repo).
				Int("phase", int(p.cursor.phase)).
				Msg("poll failed")
			failed = true
			break
		}
		budget -= n
	}

	if !failed && p.cursorExhausted() {
		p.lastSyncTime = p.cycleSince
		p.inCycle = false
		p.logger.Debug().Int("repos", len(p.repos)).Msg("poll cycle complete")
	}
}

func (p *Poller) cursorExhausted() bool {
	return p.cursor.repoIdx >= len(p.repos)
}

func (p *Poller) advanceCursor(nextPage int) {
	if nextPage > 0 {
		p.cursor.page = nextPage
		return
	}
	p.cursor.phase++
	p.cursor.page = 1
	if p.cursor.phase >= phaseCount {
		p.cursor.repoIdx++
		p.cursor.phase = phaseIssues
		p.cursor.page = 1
	}
}

func (p *Poller) fetchAndEmit(ctx context.Context, repo RepoRef, since time.Time, budget int) (int, error) {
	perPage := budget
	if perPage > 100 {
		perPage = 100
	}

	var emitted int
	var nextPage int
	var err error

	switch p.cursor.phase {
	case phaseIssues:
		emitted, nextPage, err = p.fetchIssues(ctx, repo, since, perPage)
	case phasePRs:
		emitted, nextPage, err = p.fetchPRs(ctx, repo, since, perPage)
	case phaseComments:
		emitted, nextPage, err = p.fetchComments(ctx, repo, since, perPage)
	}

	if err != nil {
		return 0, err
	}
	p.advanceCursor(nextPage)
	return emitted, nil
}

func (p *Poller) fetchIssues(ctx context.Context, repo RepoRef, since time.Time, perPage int) (int, int, error) {
	issues, nextPage, err := p.client.ListIssuesPage(ctx, repo.Owner, repo.Repo, since, p.cursor.page, perPage)
	if err != nil {
		return 0, 0, err
	}
	var emitted int
	for _, issue := range issues {
		if issue.PullRequestLinks != nil {
			continue
		}
		ev := MapPolledIssue(issue, repo.Owner, repo.Repo, since)
		p.onEvent(ev)
		emitted++
		p.logger.Debug().Str("type", ev.Type).Int("number", issue.GetNumber()).Msg("issue event")
	}
	return emitted, nextPage, nil
}

func (p *Poller) fetchPRs(ctx context.Context, repo RepoRef, since time.Time, perPage int) (int, int, error) {
	prs, nextPage, err := p.client.ListPRsPage(ctx, repo.Owner, repo.Repo, since, p.cursor.page, perPage)
	if err != nil {
		return 0, 0, err
	}
	var emitted int
	for _, pr := range prs {
		ev := MapPolledPR(pr, repo.Owner, repo.Repo, since)
		p.onEvent(ev)
		emitted++
		p.logger.Debug().Str("type", ev.Type).Int("number", pr.GetNumber()).Msg("PR event")
	}
	return emitted, nextPage, nil
}

func (p *Poller) fetchComments(ctx context.Context, repo RepoRef, since time.Time, perPage int) (int, int, error) {
	comments, nextPage, err := p.client.ListCommentsPage(ctx, repo.Owner, repo.Repo, since, p.cursor.page, perPage)
	if err != nil {
		return 0, 0, err
	}
	var emitted int
	for _, comment := range comments {
		ev := MapPolledComment(comment, repo.Owner, repo.Repo)
		p.onEvent(ev)
		emitted++
	}
	return emitted, nextPage, nil
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
