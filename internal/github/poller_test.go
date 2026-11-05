package github

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	gh "github.com/google/go-github/v68/github"
	"github.com/rs/zerolog"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

// fakeGitHub is an in-memory GitHubClient for poller tests. List methods sort
// by updated time descending, honor `since` where the real API does, and page
// with the requested page size, so tests can assert on API call counts.
type fakeGitHub struct {
	mu sync.Mutex

	labelIssues map[string][]*gh.Issue // label -> issues carrying it
	prs         []*gh.PullRequest
	issues      []*gh.Issue // time mode
	comments    []*gh.IssueComment

	err   error // returned by every list call while set
	calls map[string]int
	since []time.Time // since values received by ListIssuesByLabelPage
}

func newFakeGitHub() *fakeGitHub {
	return &fakeGitHub{labelIssues: map[string][]*gh.Issue{}, calls: map[string]int{}}
}

func (f *fakeGitHub) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

func (f *fakeGitHub) resetCounts() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = map[string]int{}
	f.since = nil
}

func (f *fakeGitHub) record(method string) error {
	f.calls[method]++
	return f.err
}

func (f *fakeGitHub) AddLabels(context.Context, string, string, int, []string) error { return nil }
func (f *fakeGitHub) RemoveLabel(context.Context, string, string, int, string) error { return nil }
func (f *fakeGitHub) CreateComment(context.Context, string, string, int, string) error {
	return nil
}
func (f *fakeGitHub) EditIssueState(context.Context, string, string, int, string) error { return nil }
func (f *fakeGitHub) ApprovePR(context.Context, string, string, int, string) error      { return nil }
func (f *fakeGitHub) AddToProject(context.Context, string, string, int, string, []ProjectField) (string, error) {
	return "", nil
}

func (f *fakeGitHub) ListIssuesByLabelPage(_ context.Context, _, _ string, labels []string, _ string, since time.Time, page, perPage int) ([]*gh.Issue, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("ListIssuesByLabelPage"); err != nil {
		return nil, 0, err
	}
	f.since = append(f.since, since)
	var all []*gh.Issue
	for _, is := range f.labelIssues[labels[0]] {
		if since.IsZero() || !is.GetUpdatedAt().Time.Before(since) {
			all = append(all, is)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].GetUpdatedAt().Time.After(all[j].GetUpdatedAt().Time) })
	return pageOf(all, page, perPage)
}

func (f *fakeGitHub) ListPRsByStatePage(_ context.Context, _, _ string, _ string, _ []string, page, perPage int) ([]*gh.PullRequest, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("ListPRsByStatePage"); err != nil {
		return nil, 0, err
	}
	all := append([]*gh.PullRequest(nil), f.prs...)
	sort.Slice(all, func(i, j int) bool { return all[i].GetUpdatedAt().Time.After(all[j].GetUpdatedAt().Time) })
	return pageOf(all, page, perPage)
}

func (f *fakeGitHub) ListIssuesPage(_ context.Context, _, _ string, since time.Time, page, perPage int) ([]*gh.Issue, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("ListIssuesPage"); err != nil {
		return nil, 0, err
	}
	var all []*gh.Issue
	for _, is := range f.issues {
		if !is.GetUpdatedAt().Time.Before(since) {
			all = append(all, is)
		}
	}
	return pageOf(all, page, perPage)
}

func (f *fakeGitHub) ListPRsPage(_ context.Context, _, _ string, since time.Time, page, perPage int) ([]*gh.PullRequest, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("ListPRsPage"); err != nil {
		return nil, 0, err
	}
	var all []*gh.PullRequest
	for _, pr := range f.prs {
		if !pr.GetUpdatedAt().Time.Before(since) {
			all = append(all, pr)
		}
	}
	return pageOf(all, page, perPage)
}

func (f *fakeGitHub) ListCommentsPage(_ context.Context, _, _ string, since time.Time, page, perPage int) ([]*gh.IssueComment, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.record("ListCommentsPage"); err != nil {
		return nil, 0, err
	}
	var all []*gh.IssueComment
	for _, c := range f.comments {
		if !c.GetUpdatedAt().Time.Before(since) {
			all = append(all, c)
		}
	}
	return pageOf(all, page, perPage)
}

// pageOf slices items for a 1-based page and reports the next page (0 = none).
func pageOf[T any](items []T, page, perPage int) ([]T, int, error) {
	start := (page - 1) * perPage
	if start >= len(items) {
		return nil, 0, nil
	}
	end := start + perPage
	next := page + 1
	if end >= len(items) {
		end = len(items)
		next = 0
	}
	return items[start:end], next, nil
}

// --- Fixtures ---

var t0 = time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

func issueAt(num int, updated time.Time, labels ...string) *gh.Issue {
	is := &gh.Issue{
		Number:    gh.Ptr(num),
		Title:     gh.Ptr(fmt.Sprintf("Issue %d", num)),
		Body:      gh.Ptr(""),
		State:     gh.Ptr("open"),
		HTMLURL:   gh.Ptr(fmt.Sprintf("https://github.com/o/r/issues/%d", num)),
		User:      &gh.User{Login: gh.Ptr("alice")},
		CreatedAt: &gh.Timestamp{Time: updated.Add(-time.Hour)},
		UpdatedAt: &gh.Timestamp{Time: updated},
	}
	for _, l := range labels {
		is.Labels = append(is.Labels, &gh.Label{Name: gh.Ptr(l)})
	}
	return is
}

func prAt(num int, updated time.Time) *gh.PullRequest {
	return &gh.PullRequest{
		Number:    gh.Ptr(num),
		Title:     gh.Ptr(fmt.Sprintf("PR %d", num)),
		Body:      gh.Ptr(""),
		State:     gh.Ptr("open"),
		HTMLURL:   gh.Ptr(fmt.Sprintf("https://github.com/o/r/pull/%d", num)),
		User:      &gh.User{Login: gh.Ptr("thien")},
		Head:      &gh.PullRequestBranch{Ref: gh.Ptr("feature")},
		Base:      &gh.PullRequestBranch{Ref: gh.Ptr("main")},
		CreatedAt: &gh.Timestamp{Time: updated.Add(-time.Hour)},
		UpdatedAt: &gh.Timestamp{Time: updated},
	}
}

// recorder collects emitted events.
type recorder struct {
	mu     sync.Mutex
	events []protocol.Event
}

func (r *recorder) onEvent(ev protocol.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recorder) take() []protocol.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	evs := r.events
	r.events = nil
	return evs
}

func numbersOf(evs []protocol.Event) []int {
	nums := make([]int, 0, len(evs))
	for _, ev := range evs {
		nums = append(nums, ev.Payload["number"].(int))
	}
	sort.Ints(nums)
	return nums
}

// noSweep asks newTestPoller for a poller that never sweeps (sweep interval 0).
const noSweep = -1

func newTestPoller(client GitHubClient, rec *recorder, cfg PollerConfig) *Poller {
	cfg.Client = client
	cfg.OnEvent = rec.onEvent
	cfg.Logger = zerolog.Nop()
	if cfg.Interval == 0 {
		cfg.Interval = 10 * time.Second
	}
	switch cfg.SweepInterval {
	case 0:
		cfg.SweepInterval = time.Hour // the config default
	case noSweep:
		cfg.SweepInterval = 0
	}
	if len(cfg.Repos) == 0 {
		cfg.Repos = []RepoRef{{Owner: "o", Repo: "r"}}
	}
	if cfg.State == "" {
		cfg.State = "open"
	}
	p := NewPoller(cfg)
	p.now = func() time.Time { return t0 }
	return p
}

// --- Label mode ---

func TestLabelModeSweepThenIncremental(t *testing.T) {
	fake := newFakeGitHub()
	// Label "a": 250 issues (3 pages). Label "b": 50 issues, 20 of them also carry "a".
	for n := 1; n <= 250; n++ {
		fake.labelIssues["a"] = append(fake.labelIssues["a"], issueAt(n, t0.Add(-time.Duration(n)*time.Minute), "a"))
	}
	for n := 231; n <= 280; n++ {
		fake.labelIssues["b"] = append(fake.labelIssues["b"], issueAt(n, t0.Add(-time.Duration(n)*time.Minute), "b"))
	}
	// A PR shows up in the issues list and must be skipped.
	pr := issueAt(999, t0.Add(-time.Second), "a")
	pr.PullRequestLinks = &gh.PullRequestLinks{}
	fake.labelIssues["a"] = append(fake.labelIssues["a"], pr)

	rec := &recorder{}
	p := newTestPoller(fake, rec, PollerConfig{Labels: []string{"a", "b"}})
	ctx := context.Background()

	// Tick 1: full sweep, every issue once, PR skipped.
	p.tick(ctx)
	evs := rec.take()
	if len(evs) != 280 {
		t.Fatalf("sweep emitted %d events, want 280", len(evs))
	}
	for _, ev := range evs {
		if ev.Type != "github.issue.matched" {
			t.Fatalf("event type %q", ev.Type)
		}
		if ev.Payload["sweep"] != true {
			t.Fatalf("sweep flag = %v during sweep", ev.Payload["sweep"])
		}
		if ev.Payload["number"].(int) == 999 {
			t.Fatal("PR row was emitted as an issue")
		}
	}
	if got := fake.count("ListIssuesByLabelPage"); got != 4 {
		t.Errorf("sweep made %d label calls, want 4 (3 pages + 1 page)", got)
	}
	if !p.watermark.Equal(t0.Add(-time.Minute)) {
		t.Errorf("watermark = %v, want newest updated_at %v", p.watermark, t0.Add(-time.Minute))
	}

	// Tick 2: nothing changed, one call per label, nothing emitted.
	fake.resetCounts()
	p.tick(ctx)
	if evs := rec.take(); len(evs) != 0 {
		t.Fatalf("incremental tick emitted %d events, want 0", len(evs))
	}
	if got := fake.count("ListIssuesByLabelPage"); got != 2 {
		t.Errorf("incremental tick made %d calls, want 2", got)
	}
	for _, s := range fake.since {
		if s.IsZero() || s.After(p.watermark) {
			t.Errorf("since %v not within watermark %v", s, p.watermark)
		}
	}

	// Tick 3: issue 5 was updated; only it is emitted, without the sweep flag.
	fake.mu.Lock()
	fake.labelIssues["a"][4].UpdatedAt = &gh.Timestamp{Time: t0.Add(time.Minute)}
	fake.mu.Unlock()
	p.tick(ctx)
	evs = rec.take()
	if got := numbersOf(evs); len(got) != 1 || got[0] != 5 {
		t.Fatalf("after update emitted %v, want [5]", got)
	}
	if evs[0].Payload["sweep"] != false {
		t.Errorf("sweep flag = %v on incremental emission", evs[0].Payload["sweep"])
	}
	if !p.watermark.Equal(t0.Add(time.Minute)) {
		t.Errorf("watermark = %v, want %v", p.watermark, t0.Add(time.Minute))
	}
}

func TestLabelModeORSemantics(t *testing.T) {
	fake := newFakeGitHub()
	fake.labelIssues["bug"] = []*gh.Issue{issueAt(1, t0, "bug")}
	fake.labelIssues["urgent"] = []*gh.Issue{issueAt(2, t0, "urgent")}
	rec := &recorder{}
	p := newTestPoller(fake, rec, PollerConfig{Labels: []string{"bug", "urgent"}})
	p.tick(context.Background())
	if got := numbersOf(rec.take()); fmt.Sprint(got) != "[1 2]" {
		t.Fatalf("emitted %v, want [1 2]", got)
	}
}

// --- PR-match mode ---

func TestPRMatchModeStopsAtWatermark(t *testing.T) {
	fake := newFakeGitHub()
	for n := 1; n <= 150; n++ {
		fake.prs = append(fake.prs, prAt(n, t0.Add(-time.Duration(n)*time.Minute)))
	}
	rec := &recorder{}
	p := newTestPoller(fake, rec, PollerConfig{MatchPRs: true})
	ctx := context.Background()

	p.tick(ctx)
	evs := rec.take()
	if len(evs) != 150 || evs[0].Type != "github.pr.matched" {
		t.Fatalf("sweep emitted %d %s events, want 150 github.pr.matched", len(evs), evs[0].Type)
	}
	if got := fake.count("ListPRsByStatePage"); got != 2 {
		t.Errorf("sweep made %d calls, want 2", got)
	}

	// The PR list API has no since filter: the poller must stop after the
	// first page once it sees an item older than the watermark.
	fake.resetCounts()
	p.tick(ctx)
	if evs := rec.take(); len(evs) != 0 {
		t.Fatalf("incremental tick emitted %d events, want 0", len(evs))
	}
	if got := fake.count("ListPRsByStatePage"); got != 1 {
		t.Errorf("incremental tick made %d calls, want 1", got)
	}

	// New commits on PR 3 bump its updated time: emitted again.
	fake.mu.Lock()
	fake.prs[2].UpdatedAt = &gh.Timestamp{Time: t0.Add(time.Minute)}
	fake.mu.Unlock()
	p.tick(ctx)
	if got := numbersOf(rec.take()); fmt.Sprint(got) != "[3]" {
		t.Fatalf("after update emitted %v, want [3]", got)
	}

	// A brand new PR is emitted on the next tick.
	fake.mu.Lock()
	fake.prs = append(fake.prs, prAt(151, t0.Add(2*time.Minute)))
	fake.mu.Unlock()
	p.tick(ctx)
	if got := numbersOf(rec.take()); fmt.Sprint(got) != "[151]" {
		t.Fatalf("new PR emitted %v, want [151]", got)
	}
}

// --- Sweeps ---

func TestSweepIntervalRepeatsSweep(t *testing.T) {
	fake := newFakeGitHub()
	fake.labelIssues["a"] = []*gh.Issue{issueAt(1, t0.Add(-time.Minute), "a"), issueAt(2, t0.Add(-2*time.Minute), "a")}
	rec := &recorder{}
	p := newTestPoller(fake, rec, PollerConfig{Labels: []string{"a"}, SweepInterval: time.Hour})
	ctx := context.Background()

	now := t0
	p.now = func() time.Time { return now }

	p.tick(ctx) // startup sweep
	if len(rec.take()) != 2 {
		t.Fatal("startup sweep did not emit both issues")
	}

	now = t0.Add(10 * time.Second)
	p.tick(ctx) // incremental: nothing changed
	if len(rec.take()) != 0 {
		t.Fatal("incremental tick re-emitted unchanged issues")
	}

	now = t0.Add(time.Hour + time.Second)
	p.tick(ctx) // periodic sweep
	evs := rec.take()
	if len(evs) != 2 {
		t.Fatalf("periodic sweep emitted %d events, want 2", len(evs))
	}
	if evs[0].Payload["sweep"] != true {
		t.Error("periodic sweep did not set the sweep flag")
	}
	if !p.lastSweep.Equal(now) {
		t.Errorf("lastSweep = %v, want %v", p.lastSweep, now)
	}
}

func TestSweepIntervalZeroNeverSweeps(t *testing.T) {
	fake := newFakeGitHub()
	recent := issueAt(1, t0.Add(-time.Minute), "a") // inside the one-hour startup lookback
	old := issueAt(2, t0.Add(-2*time.Hour), "a")    // outside it
	fake.labelIssues["a"] = []*gh.Issue{recent, old}
	rec := &recorder{}
	p := newTestPoller(fake, rec, PollerConfig{Labels: []string{"a"}, SweepInterval: noSweep})
	now := t0
	p.now = func() time.Time { return now }
	ctx := context.Background()

	// First run, no saved state: not a sweep, only the last hour is fetched.
	p.tick(ctx)
	evs := rec.take()
	if got := numbersOf(evs); fmt.Sprint(got) != "[1]" {
		t.Fatalf("first tick emitted %v, want [1]", got)
	}
	if evs[0].Payload["sweep"] != false {
		t.Error("first tick without sweeps was flagged as a sweep")
	}

	// Much later: still no sweep.
	now = t0.Add(48 * time.Hour)
	p.tick(ctx)
	if evs := rec.take(); len(evs) != 0 {
		t.Fatalf("sweep ran with sweep_interval 0: %d events", len(evs))
	}

	// Changes are still picked up incrementally.
	fake.mu.Lock()
	old.UpdatedAt = &gh.Timestamp{Time: now.Add(-time.Second)}
	fake.mu.Unlock()
	p.tick(ctx)
	if got := numbersOf(rec.take()); fmt.Sprint(got) != "[2]" {
		t.Fatalf("after update emitted %v, want [2]", got)
	}

	// A settings change does not sweep either; it restarts one hour back.
	p.Update(PollerConfig{Interval: 10 * time.Second, SweepInterval: 0, Repos: []RepoRef{{Owner: "o", Repo: "r"}}, Labels: []string{"a", "b"}, State: "open"})
	p.tick(ctx)
	evs = rec.take()
	if got := numbersOf(evs); fmt.Sprint(got) != "[2]" {
		t.Fatalf("after settings change emitted %v, want [2] (only the last hour)", got)
	}
	if evs[0].Payload["sweep"] != false {
		t.Error("settings change triggered a sweep with sweep_interval 0")
	}
}

// --- State persistence ---

func TestStatePersistsAcrossRestart(t *testing.T) {
	fake := newFakeGitHub()
	for n := 1; n <= 3; n++ {
		fake.labelIssues["a"] = append(fake.labelIssues["a"], issueAt(n, t0.Add(-time.Duration(n)*time.Minute), "a"))
	}
	state := filepath.Join(t.TempDir(), "poll.json")
	cfg := PollerConfig{Labels: []string{"a"}, StateFile: state}
	ctx := context.Background()

	rec1 := &recorder{}
	p1 := newTestPoller(fake, rec1, cfg)
	p1.tick(ctx)
	if len(rec1.take()) != 3 {
		t.Fatal("first run did not sweep all issues")
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("state file not written: %v", err)
	}

	// "Restart": a new poller on the same state file resumes without sweeping.
	rec2 := &recorder{}
	p2 := newTestPoller(fake, rec2, cfg)
	p2.now = func() time.Time { return t0.Add(10 * time.Second) }
	if !p2.watermark.Equal(p1.watermark) {
		t.Fatalf("restored watermark %v, want %v", p2.watermark, p1.watermark)
	}
	if !p2.lastSweep.Equal(t0) {
		t.Errorf("restored lastSweep %v, want %v", p2.lastSweep, t0)
	}
	fake.resetCounts()
	p2.tick(ctx)
	if evs := rec2.take(); len(evs) != 0 {
		t.Fatalf("restart re-emitted %d unchanged issues", len(evs))
	}
	if got := fake.count("ListIssuesByLabelPage"); got != 1 {
		t.Errorf("restart made %d calls, want 1 incremental call", got)
	}

	// Changes made while "down" are picked up on the first tick.
	fake.mu.Lock()
	fake.labelIssues["a"][1].UpdatedAt = &gh.Timestamp{Time: t0.Add(5 * time.Second)}
	fake.mu.Unlock()
	p2.tick(ctx)
	if got := numbersOf(rec2.take()); fmt.Sprint(got) != "[2]" {
		t.Fatalf("after restart emitted %v, want [2]", got)
	}

	// The periodic sweep is still due an hour after the saved sweep time.
	p2.now = func() time.Time { return t0.Add(time.Hour + time.Second) }
	p2.tick(ctx)
	if evs := rec2.take(); len(evs) != 3 || evs[0].Payload["sweep"] != true {
		t.Fatalf("periodic sweep after restart emitted %d events (sweep=%v), want 3", len(evs), evs[0].Payload["sweep"])
	}
}

func TestRestoredStateWithoutSweepTimeSweepsWhenEnabled(t *testing.T) {
	// A state file written by a never-sweep run has no last_sweep; turning
	// sweeps on must still produce one.
	fake := newFakeGitHub()
	fake.labelIssues["a"] = []*gh.Issue{issueAt(1, t0.Add(-time.Minute), "a")}
	state := filepath.Join(t.TempDir(), "poll.json")
	if err := os.WriteFile(state, []byte(fmt.Sprintf(`{"watermark":%q}`, t0.Format(time.RFC3339Nano))), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	p := newTestPoller(fake, rec, PollerConfig{Labels: []string{"a"}, StateFile: state, SweepInterval: time.Hour})
	p.tick(context.Background())
	evs := rec.take()
	if len(evs) != 1 || evs[0].Payload["sweep"] != true {
		t.Fatalf("expected a sweep when sweeps are enabled and none was ever recorded; got %d events", len(evs))
	}
}

func TestCorruptStateFileStartsFresh(t *testing.T) {
	fake := newFakeGitHub()
	fake.labelIssues["a"] = []*gh.Issue{issueAt(1, t0.Add(-time.Minute), "a")}
	state := filepath.Join(t.TempDir(), "poll.json")
	if err := os.WriteFile(state, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	p := newTestPoller(fake, rec, PollerConfig{Labels: []string{"a"}, StateFile: state})
	p.tick(context.Background())
	if len(rec.take()) != 1 {
		t.Fatal("corrupt state should fall back to a startup sweep")
	}
	data, err := os.ReadFile(state)
	if err != nil || len(data) == 0 || string(data) == "not json" {
		t.Fatalf("state file not rewritten after the tick: %q, %v", data, err)
	}
}

// --- Errors and backoff ---

func TestErrorKeepsWatermarkAndBacksOff(t *testing.T) {
	fake := newFakeGitHub()
	fake.labelIssues["a"] = []*gh.Issue{issueAt(1, t0.Add(-time.Minute), "a")}
	rec := &recorder{}
	p := newTestPoller(fake, rec, PollerConfig{Labels: []string{"a"}, Interval: 10 * time.Second})
	now := t0
	p.now = func() time.Time { return now }
	ctx := context.Background()

	p.tick(ctx)
	rec.take()
	wm := p.watermark

	// Tick 2 fails: watermark unchanged, backoff scheduled (2 * interval).
	fake.mu.Lock()
	fake.err = errors.New("boom")
	fake.mu.Unlock()
	now = t0.Add(10 * time.Second)
	p.tick(ctx)
	if !p.watermark.Equal(wm) {
		t.Errorf("watermark moved on error: %v -> %v", wm, p.watermark)
	}
	if want := now.Add(20 * time.Second); !p.backoffUntil.Equal(want) {
		t.Errorf("backoffUntil = %v, want %v", p.backoffUntil, want)
	}

	// Tick 3 inside the backoff window: no API call at all.
	fake.resetCounts()
	now = t0.Add(20 * time.Second)
	p.tick(ctx)
	if got := fake.count("ListIssuesByLabelPage"); got != 0 {
		t.Errorf("tick during backoff made %d calls, want 0", got)
	}

	// Second consecutive failure doubles the wait.
	now = t0.Add(31 * time.Second)
	p.tick(ctx)
	if want := now.Add(40 * time.Second); !p.backoffUntil.Equal(want) {
		t.Errorf("backoffUntil after 2 failures = %v, want %v", p.backoffUntil, want)
	}

	// Recovery: the next successful tick resets the failure count and emits changes.
	fake.mu.Lock()
	fake.err = nil
	fake.labelIssues["a"][0].UpdatedAt = &gh.Timestamp{Time: t0.Add(time.Minute)}
	fake.mu.Unlock()
	now = t0.Add(2 * time.Minute)
	p.tick(ctx)
	if got := numbersOf(rec.take()); fmt.Sprint(got) != "[1]" {
		t.Fatalf("after recovery emitted %v, want [1]", got)
	}
	if p.failures != 0 {
		t.Errorf("failures = %d after success, want 0", p.failures)
	}
}

func TestRateLimitErrorWaitsForReset(t *testing.T) {
	fake := newFakeGitHub()
	rec := &recorder{}
	p := newTestPoller(fake, rec, PollerConfig{Labels: []string{"a"}})
	reset := t0.Add(7 * time.Minute)
	fake.err = &gh.RateLimitError{Rate: gh.Rate{Reset: gh.Timestamp{Time: reset}}, Message: "rate limited"}

	p.tick(context.Background())
	if want := reset.Add(time.Second); !p.backoffUntil.Equal(want) {
		t.Errorf("backoffUntil = %v, want reset+1s %v", p.backoffUntil, want)
	}
}

func TestAbuseRateLimitHonorsRetryAfter(t *testing.T) {
	fake := newFakeGitHub()
	rec := &recorder{}
	p := newTestPoller(fake, rec, PollerConfig{Labels: []string{"a"}})
	retry := 90 * time.Second
	fake.err = &gh.AbuseRateLimitError{RetryAfter: &retry, Message: "slow down"}

	p.tick(context.Background())
	if want := t0.Add(retry); !p.backoffUntil.Equal(want) {
		t.Errorf("backoffUntil = %v, want %v", p.backoffUntil, want)
	}
}

func TestBackoffIsCapped(t *testing.T) {
	fake := newFakeGitHub()
	rec := &recorder{}
	p := newTestPoller(fake, rec, PollerConfig{Labels: []string{"a"}, Interval: 10 * time.Minute})
	fake.err = errors.New("down")
	now := t0
	p.now = func() time.Time { return now }
	for i := 0; i < 6; i++ {
		now = p.backoffUntil.Add(time.Second)
		p.tick(context.Background())
	}
	if got := p.backoffUntil.Sub(now); got != maxBackoff {
		t.Errorf("backoff after repeated failures = %v, want cap %v", got, maxBackoff)
	}
}

// --- Config reload ---

func TestUpdateChangesSettingsAndForcesSweep(t *testing.T) {
	fake := newFakeGitHub()
	fake.labelIssues["a"] = []*gh.Issue{issueAt(1, t0.Add(-time.Minute), "a")}
	fake.labelIssues["b"] = []*gh.Issue{issueAt(2, t0.Add(-time.Minute), "b")}
	rec := &recorder{}
	p := newTestPoller(fake, rec, PollerConfig{Labels: []string{"a"}, Interval: 10 * time.Second})
	ctx := context.Background()

	p.tick(ctx)
	if got := numbersOf(rec.take()); fmt.Sprint(got) != "[1]" {
		t.Fatalf("initial sweep emitted %v, want [1]", got)
	}

	p.Update(PollerConfig{
		Interval: 20 * time.Second, SweepInterval: time.Hour,
		Repos: []RepoRef{{Owner: "o", Repo: "r"}}, Labels: []string{"b"}, State: "open",
	})
	select {
	case d := <-p.intervalCh:
		if d != 20*time.Second {
			t.Errorf("interval signal = %v, want 20s", d)
		}
	default:
		t.Error("interval change did not signal Run")
	}
	if !p.watermark.IsZero() {
		t.Error("label change did not reset the watermark")
	}

	p.tick(ctx)
	evs := rec.take()
	if got := numbersOf(evs); fmt.Sprint(got) != "[2]" {
		t.Fatalf("after label change emitted %v, want [2]", got)
	}
	if evs[0].Payload["sweep"] != true {
		t.Error("tick after settings change was not a sweep")
	}
}

func TestUpdateSameSettingsKeepsWatermark(t *testing.T) {
	fake := newFakeGitHub()
	fake.labelIssues["a"] = []*gh.Issue{issueAt(1, t0.Add(-time.Minute), "a")}
	rec := &recorder{}
	cfg := PollerConfig{Labels: []string{"a"}, Interval: 10 * time.Second, Repos: []RepoRef{{Owner: "o", Repo: "r"}}, State: "open"}
	p := newTestPoller(fake, rec, cfg)
	p.tick(context.Background())
	rec.take()
	wm := p.watermark

	p.Update(cfg)
	if !p.watermark.Equal(wm) {
		t.Error("identical settings reset the watermark")
	}
	select {
	case <-p.intervalCh:
		t.Error("unchanged interval signalled Run")
	default:
	}
}

// --- Time mode ---

func TestTimeModeEmitsOpenedUpdatedAndComments(t *testing.T) {
	fake := newFakeGitHub()
	since := t0.Add(-5 * time.Minute)

	opened := issueAt(10, t0.Add(-time.Minute))
	opened.CreatedAt = &gh.Timestamp{Time: t0.Add(-2 * time.Minute)} // created after since
	updated := issueAt(11, t0.Add(-time.Minute))
	updated.CreatedAt = &gh.Timestamp{Time: t0.Add(-24 * time.Hour)} // created before since
	old := issueAt(12, t0.Add(-time.Hour))                           // not updated since the watermark
	prRow := issueAt(13, t0.Add(-time.Minute))
	prRow.PullRequestLinks = &gh.PullRequestLinks{}
	fake.issues = []*gh.Issue{opened, updated, old, prRow}

	newPR := prAt(20, t0.Add(-time.Minute))
	newPR.CreatedAt = &gh.Timestamp{Time: t0.Add(-time.Minute)}
	fake.prs = []*gh.PullRequest{newPR}

	fake.comments = []*gh.IssueComment{{
		ID:        gh.Ptr(int64(1)),
		Body:      gh.Ptr("hello"),
		User:      &gh.User{Login: gh.Ptr("bob")},
		UpdatedAt: &gh.Timestamp{Time: t0.Add(-time.Minute)},
		IssueURL:  gh.Ptr("https://api.github.com/repos/o/r/issues/10"),
	}}

	rec := &recorder{}
	p := newTestPoller(fake, rec, PollerConfig{})
	p.lastSync = since

	p.tick(context.Background())
	evs := rec.take()
	types := map[string]int{}
	for _, ev := range evs {
		types[ev.Type]++
	}
	want := map[string]int{"github.issue.opened": 1, "github.issue.updated": 1, "github.pr.opened": 1, "github.comment.created": 1}
	for k, v := range want {
		if types[k] != v {
			t.Errorf("%s: got %d, want %d (all: %v)", k, types[k], v, types)
		}
	}
	if len(evs) != 4 {
		t.Errorf("emitted %d events, want 4: %v", len(evs), types)
	}
	if !p.lastSync.Equal(t0) {
		t.Errorf("lastSync = %v, want tick time %v", p.lastSync, t0)
	}

	// Error keeps lastSync.
	fake.mu.Lock()
	fake.err = errors.New("boom")
	fake.mu.Unlock()
	p.now = func() time.Time { return t0.Add(time.Minute) }
	p.tick(context.Background())
	if !p.lastSync.Equal(t0) {
		t.Errorf("lastSync moved on error: %v", p.lastSync)
	}
}

func TestTimeModeMultipleRepos(t *testing.T) {
	fake := newFakeGitHub()
	fake.issues = []*gh.Issue{issueAt(1, t0.Add(-time.Minute))}
	rec := &recorder{}
	p := newTestPoller(fake, rec, PollerConfig{Repos: []RepoRef{{Owner: "a", Repo: "x"}, {Owner: "b", Repo: "y"}}})
	p.lastSync = t0.Add(-5 * time.Minute)
	p.tick(context.Background())
	evs := rec.take()
	if len(evs) != 2 {
		t.Fatalf("emitted %d events for 2 repos, want 2", len(evs))
	}
	owners := map[string]bool{}
	for _, ev := range evs {
		owners[ev.Payload["owner"].(string)] = true
	}
	if !owners["a"] || !owners["b"] {
		t.Errorf("owners = %v, want a and b", owners)
	}
}

// --- Misc ---

func TestRunStopsOnContextCancel(t *testing.T) {
	fake := newFakeGitHub()
	rec := &recorder{}
	p := newTestPoller(fake, rec, PollerConfig{Labels: []string{"a"}, Interval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestParseRepos(t *testing.T) {
	tests := []struct {
		name    string
		input   []string
		wantN   int
		wantErr bool
	}{
		{"valid single", []string{"owner/repo"}, 1, false},
		{"valid multiple", []string{"a/b", "c/d"}, 2, false},
		{"empty list", []string{}, 0, false},
		{"missing slash", []string{"noslash"}, 0, true},
		{"empty owner", []string{"/repo"}, 0, true},
		{"empty repo", []string{"owner/"}, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			refs, err := ParseRepos(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(refs) != tt.wantN {
				t.Errorf("got %d refs, want %d", len(refs), tt.wantN)
			}
		})
	}
}
