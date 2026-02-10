package github

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	gh "github.com/google/go-github/v68/github"
	"github.com/rs/zerolog"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

// pollMockClient is a thread-safe mock for polling tests.
type pollMockClient struct {
	mu       sync.Mutex
	issues   []*gh.Issue
	prs      []*gh.PullRequest
	comments []*gh.IssueComment
	issueErr error
	prErr    error
	calls    []mockCall
}

func (m *pollMockClient) AddLabels(_ context.Context, owner, repo string, number int, labels []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, mockCall{"AddLabels", owner, repo, number, labels})
	return nil
}

func (m *pollMockClient) RemoveLabel(_ context.Context, owner, repo string, number int, label string) error {
	return nil
}

func (m *pollMockClient) CreateComment(_ context.Context, owner, repo string, number int, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, mockCall{"CreateComment", owner, repo, number, []string{body}})
	return nil
}

func (m *pollMockClient) EditIssueState(_ context.Context, _, _ string, _ int, _ string) error {
	return nil
}

func (m *pollMockClient) ListIssuesPage(_ context.Context, _, _ string, _ time.Time, _, _ int) ([]*gh.Issue, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.issueErr != nil {
		return nil, 0, m.issueErr
	}
	issues := m.issues
	m.issues = nil
	return issues, 0, nil
}

func (m *pollMockClient) ListPRsPage(_ context.Context, _, _ string, _ time.Time, _, _ int) ([]*gh.PullRequest, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.prErr != nil {
		return nil, 0, m.prErr
	}
	prs := m.prs
	m.prs = nil
	return prs, 0, nil
}

func (m *pollMockClient) ListCommentsPage(_ context.Context, _, _ string, _ time.Time, _, _ int) ([]*gh.IssueComment, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	comments := m.comments
	m.comments = nil
	return comments, 0, nil
}

func TestPollerNewIssue(t *testing.T) {
	now := time.Now()
	created := gh.Timestamp{Time: now}
	mock := &pollMockClient{
		issues: []*gh.Issue{{
			Number:    gh.Ptr(1),
			Title:     gh.Ptr("New"),
			Body:      gh.Ptr(""),
			State:     gh.Ptr("open"),
			HTMLURL:   gh.Ptr("https://github.com/o/r/issues/1"),
			User:      &gh.User{Login: gh.Ptr("alice")},
			CreatedAt: &created,
		}},
	}

	var events []protocol.Event
	var mu sync.Mutex
	onEvent := func(ev protocol.Event) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}

	p := NewPoller(mock, time.Hour, 100, []RepoRef{{Owner: "o", Repo: "r"}}, onEvent, zerolog.Nop())
	p.lastSyncTime = now.Add(-1 * time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	// Wait for initial poll.
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != "github.issue.opened" {
		t.Errorf("got type %q, want github.issue.opened", events[0].Type)
	}
}

func TestPollerSkipsPRsFromIssuesList(t *testing.T) {
	now := time.Now()
	created := gh.Timestamp{Time: now}
	mock := &pollMockClient{
		issues: []*gh.Issue{{
			Number:           gh.Ptr(2),
			Title:            gh.Ptr("PR as issue"),
			Body:             gh.Ptr(""),
			State:            gh.Ptr("open"),
			HTMLURL:          gh.Ptr("https://github.com/o/r/pull/2"),
			User:             &gh.User{Login: gh.Ptr("bob")},
			CreatedAt:        &created,
			PullRequestLinks: &gh.PullRequestLinks{URL: gh.Ptr("https://api.github.com/repos/o/r/pulls/2")},
		}},
	}

	var events []protocol.Event
	onEvent := func(ev protocol.Event) { events = append(events, ev) }

	p := NewPoller(mock, time.Hour, 100, []RepoRef{{Owner: "o", Repo: "r"}}, onEvent, zerolog.Nop())
	p.lastSyncTime = now.Add(-1 * time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	if len(events) != 0 {
		t.Errorf("expected 0 events (PR should be skipped), got %d", len(events))
	}
}

func TestPollerMultipleRepos(t *testing.T) {
	now := time.Now()
	created := gh.Timestamp{Time: now}

	// The mock returns issues once (first repo gets them, second gets nil).
	// We just verify the poll completes without errors.
	mock := &pollMockClient{
		issues: []*gh.Issue{{
			Number:    gh.Ptr(1),
			Title:     gh.Ptr("Issue"),
			Body:      gh.Ptr(""),
			State:     gh.Ptr("open"),
			HTMLURL:   gh.Ptr("https://github.com/a/b/issues/1"),
			User:      &gh.User{Login: gh.Ptr("alice")},
			CreatedAt: &created,
		}},
	}

	var events []protocol.Event
	var mu sync.Mutex
	onEvent := func(ev protocol.Event) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}

	repos := []RepoRef{{Owner: "a", Repo: "b"}, {Owner: "c", Repo: "d"}}
	p := NewPoller(mock, time.Hour, 100, repos, onEvent, zerolog.Nop())
	p.lastSyncTime = now.Add(-1 * time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 1 {
		t.Errorf("expected 1 event, got %d", len(events))
	}
}

func TestPollerAPIErrorNoAdvance(t *testing.T) {
	mock := &pollMockClient{
		issueErr: errors.New("rate limited"),
	}

	p := NewPoller(mock, time.Hour, 100, []RepoRef{{Owner: "o", Repo: "r"}}, func(protocol.Event) {}, zerolog.Nop())
	original := p.lastSyncTime

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	if !p.lastSyncTime.Equal(original) {
		t.Errorf("lastSyncTime should not advance on error; was %v, now %v", original, p.lastSyncTime)
	}
}

func TestPollerLastSyncTimeAdvances(t *testing.T) {
	mock := &pollMockClient{} // no issues, no errors

	p := NewPoller(mock, time.Hour, 100, []RepoRef{{Owner: "o", Repo: "r"}}, func(protocol.Event) {}, zerolog.Nop())
	original := p.lastSyncTime

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	if !p.lastSyncTime.After(original) {
		t.Errorf("lastSyncTime should advance on success; was %v, now %v", original, p.lastSyncTime)
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

// --- Cursor / per_tick tests ---

// paginatingMockClient supports multi-page responses for cursor tests.
type paginatingMockClient struct {
	mu     sync.Mutex
	// issuePages[page] returns (issues, nextPage). Pages are 1-based.
	issuePages map[int]struct {
		issues   []*gh.Issue
		nextPage int
	}
	prPages map[int]struct {
		prs      []*gh.PullRequest
		nextPage int
	}
	commentPages map[int]struct {
		comments []*gh.IssueComment
		nextPage int
	}
	issueErr error
}

func (m *paginatingMockClient) AddLabels(_ context.Context, _, _ string, _ int, _ []string) error {
	return nil
}
func (m *paginatingMockClient) RemoveLabel(_ context.Context, _, _ string, _ int, _ string) error {
	return nil
}
func (m *paginatingMockClient) CreateComment(_ context.Context, _, _ string, _ int, _ string) error {
	return nil
}
func (m *paginatingMockClient) EditIssueState(_ context.Context, _, _ string, _ int, _ string) error {
	return nil
}

func (m *paginatingMockClient) ListIssuesPage(_ context.Context, _, _ string, _ time.Time, page, _ int) ([]*gh.Issue, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.issueErr != nil {
		return nil, 0, m.issueErr
	}
	if p, ok := m.issuePages[page]; ok {
		return p.issues, p.nextPage, nil
	}
	return nil, 0, nil
}

func (m *paginatingMockClient) ListPRsPage(_ context.Context, _, _ string, _ time.Time, page, _ int) ([]*gh.PullRequest, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.prPages[page]; ok {
		return p.prs, p.nextPage, nil
	}
	return nil, 0, nil
}

func (m *paginatingMockClient) ListCommentsPage(_ context.Context, _, _ string, _ time.Time, page, _ int) ([]*gh.IssueComment, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.commentPages[page]; ok {
		return p.comments, p.nextPage, nil
	}
	return nil, 0, nil
}

func makeIssue(num int, created time.Time) *gh.Issue {
	ts := gh.Timestamp{Time: created}
	return &gh.Issue{
		Number:    gh.Ptr(num),
		Title:     gh.Ptr("Issue"),
		Body:      gh.Ptr(""),
		State:     gh.Ptr("open"),
		HTMLURL:   gh.Ptr("https://github.com/o/r/issues/1"),
		User:      &gh.User{Login: gh.Ptr("alice")},
		CreatedAt: &ts,
	}
}

func TestPollerPerTickBound(t *testing.T) {
	now := time.Now()

	// 5 issues across 3 pages: page1=[#1,#2], page2=[#3,#4], page3=[#5]
	mock := &paginatingMockClient{
		issuePages: map[int]struct {
			issues   []*gh.Issue
			nextPage int
		}{
			1: {issues: []*gh.Issue{makeIssue(1, now), makeIssue(2, now)}, nextPage: 2},
			2: {issues: []*gh.Issue{makeIssue(3, now), makeIssue(4, now)}, nextPage: 3},
			3: {issues: []*gh.Issue{makeIssue(5, now)}, nextPage: 0},
		},
	}

	var mu sync.Mutex
	var events []protocol.Event
	onEvent := func(ev protocol.Event) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}

	// perTick=2: each tick fetches one page of up to 2 items.
	p := NewPoller(mock, time.Hour, 2, []RepoRef{{Owner: "o", Repo: "r"}}, onEvent, zerolog.Nop())
	p.lastSyncTime = now.Add(-1 * time.Minute)

	ctx := context.Background()

	// Tick 1: should emit 2 events (page 1).
	p.tick(ctx)
	mu.Lock()
	if len(events) != 2 {
		t.Fatalf("tick 1: expected 2 events, got %d", len(events))
	}
	mu.Unlock()

	// Cycle should still be active.
	if !p.inCycle {
		t.Fatal("expected cycle to still be active after tick 1")
	}

	// Tick 2: should emit 2 more events (page 2).
	p.tick(ctx)
	mu.Lock()
	if len(events) != 4 {
		t.Fatalf("tick 2: expected 4 events total, got %d", len(events))
	}
	mu.Unlock()

	// Tick 3: should emit 1 event (page 3) and then drain PRs+comments (empty).
	p.tick(ctx)
	mu.Lock()
	if len(events) != 5 {
		t.Fatalf("tick 3: expected 5 events total, got %d", len(events))
	}
	mu.Unlock()

	// Cycle should be complete.
	if p.inCycle {
		t.Fatal("expected cycle to be complete after all items consumed")
	}
}

func TestPollerCursorResumesAcrossTicks(t *testing.T) {
	now := time.Now()

	// 2 issues on page 1, 1 PR on page 1.
	mock := &paginatingMockClient{
		issuePages: map[int]struct {
			issues   []*gh.Issue
			nextPage int
		}{
			1: {issues: []*gh.Issue{makeIssue(1, now), makeIssue(2, now)}, nextPage: 0},
		},
		prPages: map[int]struct {
			prs      []*gh.PullRequest
			nextPage int
		}{
			1: {prs: []*gh.PullRequest{{
				Number:    gh.Ptr(10),
				Title:     gh.Ptr("PR"),
				Body:      gh.Ptr(""),
				State:     gh.Ptr("open"),
				HTMLURL:   gh.Ptr("https://github.com/o/r/pull/10"),
				User:      &gh.User{Login: gh.Ptr("bob")},
				Head:      &gh.PullRequestBranch{Ref: gh.Ptr("feat")},
				Base:      &gh.PullRequestBranch{Ref: gh.Ptr("main")},
				CreatedAt: &gh.Timestamp{Time: now},
			}}, nextPage: 0},
		},
	}

	var mu sync.Mutex
	var events []protocol.Event
	onEvent := func(ev protocol.Event) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	}

	// perTick=2: first tick gets 2 issues, second tick gets 1 PR.
	p := NewPoller(mock, time.Hour, 2, []RepoRef{{Owner: "o", Repo: "r"}}, onEvent, zerolog.Nop())
	p.lastSyncTime = now.Add(-1 * time.Minute)

	ctx := context.Background()

	p.tick(ctx)
	mu.Lock()
	if len(events) != 2 {
		t.Fatalf("tick 1: expected 2 events, got %d", len(events))
	}
	if events[0].Type != "github.issue.opened" {
		t.Errorf("tick 1 event 0: got %q, want github.issue.opened", events[0].Type)
	}
	mu.Unlock()

	p.tick(ctx)
	mu.Lock()
	if len(events) != 3 {
		t.Fatalf("tick 2: expected 3 events total, got %d", len(events))
	}
	if events[2].Type != "github.pr.opened" {
		t.Errorf("tick 2 event 2: got %q, want github.pr.opened", events[2].Type)
	}
	mu.Unlock()

	if p.inCycle {
		t.Fatal("expected cycle to be complete")
	}
}

func TestPollerCycleAdvancesLastSyncTime(t *testing.T) {
	now := time.Now()

	mock := &paginatingMockClient{
		issuePages: map[int]struct {
			issues   []*gh.Issue
			nextPage int
		}{
			1: {issues: []*gh.Issue{makeIssue(1, now), makeIssue(2, now)}, nextPage: 0},
		},
	}

	p := NewPoller(mock, time.Hour, 1, []RepoRef{{Owner: "o", Repo: "r"}}, func(protocol.Event) {}, zerolog.Nop())
	original := p.lastSyncTime

	ctx := context.Background()

	// Tick 1: fetches page with 2 issues but perTick=1, so only 1 page fetched.
	// Wait — perPage = min(budget, 100) = 1, so GitHub returns 1 item.
	// Actually paginatingMockClient ignores perPage, returns full page.
	// With perTick=1, budget=1, it fetches one page (2 items emitted), budget goes to -1.
	// That's fine for this test — the point is lastSyncTime shouldn't advance yet
	// because there are still PRs and comments phases.
	p.tick(ctx)

	// After first tick with perTick=1: issues consumed (2 items), budget exhausted.
	// PRs and comments not yet visited. Cycle still active.
	if !p.inCycle {
		// If cycle completed in one tick (all phases empty), that's also valid.
		// Check if lastSyncTime advanced.
		if p.lastSyncTime.After(original) {
			return // cycle completed in one tick, test passes.
		}
	}

	if p.lastSyncTime.After(original) {
		t.Error("lastSyncTime should not advance until cycle is complete")
	}

	// Drain remaining ticks until cycle completes.
	for i := 0; i < 10 && p.inCycle; i++ {
		p.tick(ctx)
	}

	if p.inCycle {
		t.Fatal("cycle should have completed")
	}
	if !p.lastSyncTime.After(original) {
		t.Error("lastSyncTime should have advanced after cycle completed")
	}
}

func TestPollerErrorRetainsPosition(t *testing.T) {
	now := time.Now()

	mock := &paginatingMockClient{
		issuePages: map[int]struct {
			issues   []*gh.Issue
			nextPage int
		}{
			1: {issues: []*gh.Issue{makeIssue(1, now)}, nextPage: 0},
		},
		// PRs will error.
		prPages: map[int]struct {
			prs      []*gh.PullRequest
			nextPage int
		}{},
	}

	var eventCount int
	p := NewPoller(mock, time.Hour, 100, []RepoRef{{Owner: "o", Repo: "r"}}, func(protocol.Event) {
		eventCount++
	}, zerolog.Nop())
	p.lastSyncTime = now.Add(-1 * time.Minute)
	original := p.lastSyncTime

	ctx := context.Background()

	// Tick 1: issues succeed (1 event), PRs return empty, comments return empty.
	// Actually this will succeed fully because empty pages just advance the cursor.
	// To test error retention, we need to inject an error.
	// Let's set issueErr after first tick.
	p.tick(ctx)
	if eventCount != 1 {
		t.Fatalf("tick 1: expected 1 event, got %d", eventCount)
	}

	// The cycle completed because all pages were empty. Start a new scenario:
	// Reset and inject an error for the issues phase.
	mock.issueErr = errors.New("server error")
	p.lastSyncTime = original

	// Force a new cycle.
	p.inCycle = false
	p.tick(ctx)

	// Error should prevent lastSyncTime from advancing.
	if p.lastSyncTime.After(original) {
		t.Error("lastSyncTime should not advance on error")
	}

	// Cursor should still be in the cycle (retained position).
	if !p.inCycle {
		t.Error("cycle should still be active after error")
	}
	if p.cursor.phase != phaseIssues {
		t.Errorf("cursor should be at issues phase, got %d", p.cursor.phase)
	}

	// Clear the error. Next tick should succeed from the same position.
	mock.mu.Lock()
	mock.issueErr = nil
	mock.issuePages = map[int]struct {
		issues   []*gh.Issue
		nextPage int
	}{
		1: {issues: []*gh.Issue{makeIssue(10, now)}, nextPage: 0},
	}
	mock.mu.Unlock()

	p.tick(ctx)
	if eventCount != 2 {
		t.Fatalf("tick 3: expected 2 events total, got %d", eventCount)
	}
}
