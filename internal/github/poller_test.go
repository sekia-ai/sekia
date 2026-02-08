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

func (m *pollMockClient) ListIssuesSince(_ context.Context, _, _ string, _ time.Time) ([]*gh.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.issueErr != nil {
		return nil, m.issueErr
	}
	issues := m.issues
	m.issues = nil // return once
	return issues, nil
}

func (m *pollMockClient) ListPRsSince(_ context.Context, _, _ string, _ time.Time) ([]*gh.PullRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.prErr != nil {
		return nil, m.prErr
	}
	prs := m.prs
	m.prs = nil
	return prs, nil
}

func (m *pollMockClient) ListCommentsSince(_ context.Context, _, _ string, _ time.Time) ([]*gh.IssueComment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	comments := m.comments
	m.comments = nil
	return comments, nil
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

	p := NewPoller(mock, time.Hour, []RepoRef{{Owner: "o", Repo: "r"}}, onEvent, zerolog.Nop())
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
			Number:            gh.Ptr(2),
			Title:             gh.Ptr("PR as issue"),
			Body:              gh.Ptr(""),
			State:             gh.Ptr("open"),
			HTMLURL:           gh.Ptr("https://github.com/o/r/pull/2"),
			User:              &gh.User{Login: gh.Ptr("bob")},
			CreatedAt:         &created,
			PullRequestLinks: &gh.PullRequestLinks{URL: gh.Ptr("https://api.github.com/repos/o/r/pulls/2")},
		}},
	}

	var events []protocol.Event
	onEvent := func(ev protocol.Event) { events = append(events, ev) }

	p := NewPoller(mock, time.Hour, []RepoRef{{Owner: "o", Repo: "r"}}, onEvent, zerolog.Nop())
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
	p := NewPoller(mock, time.Hour, repos, onEvent, zerolog.Nop())
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

	p := NewPoller(mock, time.Hour, []RepoRef{{Owner: "o", Repo: "r"}}, func(protocol.Event) {}, zerolog.Nop())
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

	p := NewPoller(mock, time.Hour, []RepoRef{{Owner: "o", Repo: "r"}}, func(protocol.Event) {}, zerolog.Nop())
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
