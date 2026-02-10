package github

import (
	"context"
	"fmt"
	"testing"
	"time"

	gh "github.com/google/go-github/v68/github"
)

// mockGitHubClient records all API calls for assertion.
type mockGitHubClient struct {
	calls []mockCall
}

type mockCall struct {
	Method string
	Owner  string
	Repo   string
	Number int
	Args   []string
}

func (m *mockGitHubClient) AddLabels(_ context.Context, owner, repo string, number int, labels []string) error {
	m.calls = append(m.calls, mockCall{"AddLabels", owner, repo, number, labels})
	return nil
}

func (m *mockGitHubClient) RemoveLabel(_ context.Context, owner, repo string, number int, label string) error {
	m.calls = append(m.calls, mockCall{"RemoveLabel", owner, repo, number, []string{label}})
	return nil
}

func (m *mockGitHubClient) CreateComment(_ context.Context, owner, repo string, number int, body string) error {
	m.calls = append(m.calls, mockCall{"CreateComment", owner, repo, number, []string{body}})
	return nil
}

func (m *mockGitHubClient) EditIssueState(_ context.Context, owner, repo string, number int, state string) error {
	m.calls = append(m.calls, mockCall{"EditIssueState", owner, repo, number, []string{state}})
	return nil
}

func (m *mockGitHubClient) ListIssuesPage(_ context.Context, _, _ string, _ time.Time, _, _ int) ([]*gh.Issue, int, error) {
	return nil, 0, nil
}

func (m *mockGitHubClient) ListPRsPage(_ context.Context, _, _ string, _ time.Time, _, _ int) ([]*gh.PullRequest, int, error) {
	return nil, 0, nil
}

func (m *mockGitHubClient) ListCommentsPage(_ context.Context, _, _ string, _ time.Time, _, _ int) ([]*gh.IssueComment, int, error) {
	return nil, 0, nil
}

func (m *mockGitHubClient) ListIssuesByLabelPage(_ context.Context, _, _ string, _ []string, _ string, _, _ int) ([]*gh.Issue, int, error) {
	return nil, 0, nil
}

func TestCmdAddLabel(t *testing.T) {
	mock := &mockGitHubClient{}
	err := cmdAddLabel(context.Background(), mock, map[string]any{
		"owner":  "myorg",
		"repo":   "myrepo",
		"number": float64(42),
		"label":  "bug",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mock.calls))
	}
	c := mock.calls[0]
	if c.Method != "AddLabels" || c.Owner != "myorg" || c.Repo != "myrepo" || c.Number != 42 {
		t.Errorf("unexpected call: %+v", c)
	}
	if len(c.Args) != 1 || c.Args[0] != "bug" {
		t.Errorf("unexpected labels: %v", c.Args)
	}
}

func TestCmdRemoveLabel(t *testing.T) {
	mock := &mockGitHubClient{}
	err := cmdRemoveLabel(context.Background(), mock, map[string]any{
		"owner":  "myorg",
		"repo":   "myrepo",
		"number": float64(1),
		"label":  "wontfix",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.calls[0].Method != "RemoveLabel" || mock.calls[0].Args[0] != "wontfix" {
		t.Errorf("unexpected call: %+v", mock.calls[0])
	}
}

func TestCmdCreateComment(t *testing.T) {
	mock := &mockGitHubClient{}
	err := cmdCreateComment(context.Background(), mock, map[string]any{
		"owner":  "myorg",
		"repo":   "myrepo",
		"number": float64(5),
		"body":   "Hello, world!",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.calls[0].Method != "CreateComment" || mock.calls[0].Args[0] != "Hello, world!" {
		t.Errorf("unexpected call: %+v", mock.calls[0])
	}
}

func TestCmdCloseIssue(t *testing.T) {
	mock := &mockGitHubClient{}
	err := cmdCloseIssue(context.Background(), mock, map[string]any{
		"owner":  "myorg",
		"repo":   "myrepo",
		"number": float64(10),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.calls[0].Method != "EditIssueState" || mock.calls[0].Args[0] != "closed" {
		t.Errorf("unexpected call: %+v", mock.calls[0])
	}
}

func TestCmdReopenIssue(t *testing.T) {
	mock := &mockGitHubClient{}
	err := cmdReopenIssue(context.Background(), mock, map[string]any{
		"owner":  "myorg",
		"repo":   "myrepo",
		"number": float64(10),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mock.calls[0].Method != "EditIssueState" || mock.calls[0].Args[0] != "open" {
		t.Errorf("unexpected call: %+v", mock.calls[0])
	}
}

func TestCmdMissingFields(t *testing.T) {
	mock := &mockGitHubClient{}

	tests := []struct {
		name    string
		payload map[string]any
	}{
		{"missing owner", map[string]any{"repo": "r", "number": float64(1), "label": "x"}},
		{"missing repo", map[string]any{"owner": "o", "number": float64(1), "label": "x"}},
		{"missing number", map[string]any{"owner": "o", "repo": "r", "label": "x"}},
		{"missing label", map[string]any{"owner": "o", "repo": "r", "number": float64(1)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := cmdAddLabel(context.Background(), mock, tt.payload)
			if err == nil {
				t.Error("expected error for missing field")
			}
		})
	}
}

func TestExtractRepoRefIntNumber(t *testing.T) {
	// When payload comes from Go code (not JSON), number might be int.
	owner, repo, number, err := extractRepoRef(map[string]any{
		"owner":  "o",
		"repo":   "r",
		"number": 42,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != "o" || repo != "r" || number != 42 {
		t.Errorf("got %s/%s#%d, want o/r#42", owner, repo, number)
	}
}

// Suppress unused import warning.
var _ = fmt.Sprintf
