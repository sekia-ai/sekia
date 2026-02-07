package linear_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"

	linearagent "github.com/sekia-ai/sekia/internal/linear"
	"github.com/sekia-ai/sekia/internal/server"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

// TestLinearAgentEndToEnd tests the full flow:
//
//	poller fetches issues → event on NATS → Lua workflow → command → mock Linear API
func TestLinearAgentEndToEnd(t *testing.T) {
	// 1. Create a mock Linear client that returns a fake issue and records commands.
	mock := &mockLinearClient{
		issues: []linearagent.LinearIssue{
			{
				ID:         "issue-1",
				Identifier: "ENG-42",
				Title:      "Fix the bug",
				CreatedAt:  time.Now(),
				UpdatedAt:  time.Now(),
				State:      struct{ Name string `json:"name"` }{Name: "In Progress"},
				Team:       struct{ Key string `json:"key"` }{Key: "ENG"},
				URL:        "https://linear.app/team/ENG-42",
			},
		},
	}

	// 2. Write a Lua workflow that auto-comments on new Linear issues.
	wfDir := t.TempDir()
	workflowCode := `
sekia.on("sekia.events.linear", function(event)
	if event.type ~= "linear.issue.created" then return end
	sekia.command("linear-agent", "create_comment", {
		issue_id = event.payload.id,
		body     = "auto-triaged: " .. event.payload.title,
	})
end)
`
	os.WriteFile(filepath.Join(wfDir, "auto-triage.lua"), []byte(workflowCode), 0644)

	// 3. Start sekiad daemon with workflow engine.
	d, _ := newTestDaemon(t, wfDir)

	// 4. Start the Linear agent with a fast poll interval.
	la := newTestLinearAgent(t, d, mock)

	// 5. Wait for the poller to fetch the issue, publish event, and workflow to dispatch command.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mock.mu.Lock()
		n := len(mock.commandCalls)
		mock.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()

	if len(mock.commandCalls) == 0 {
		t.Fatal("no Linear API command calls received; expected create_comment")
	}

	call := mock.commandCalls[0]
	if call.Method != "CreateComment" {
		t.Errorf("method = %s, want CreateComment", call.Method)
	}
	if call.Args["issue_id"] != "issue-1" {
		t.Errorf("issue_id = %s, want issue-1", call.Args["issue_id"])
	}
	if call.Args["body"] != "auto-triaged: Fix the bug" {
		t.Errorf("body = %s, want 'auto-triaged: Fix the bug'", call.Args["body"])
	}

	_ = la // keep reference
}

// TestLinearAgentCommandHandling tests that commands received via NATS
// are correctly dispatched to the Linear API.
func TestLinearAgentCommandHandling(t *testing.T) {
	mock := &mockLinearClient{}
	d, _ := newTestDaemon(t, "")
	la := newTestLinearAgent(t, d, mock)

	time.Sleep(800 * time.Millisecond)

	nc, err := nats.Connect(d.NATSClientURL(), d.NATSConnectOpts()...)
	if err != nil {
		t.Fatalf("connect nats: %v", err)
	}
	defer nc.Drain()

	// Send a create_issue command.
	cmd := map[string]any{
		"command": "create_issue",
		"payload": map[string]any{
			"team_id":     "team-1",
			"title":       "New issue",
			"description": "A description",
		},
		"source": "test",
	}
	cmdData, _ := json.Marshal(cmd)
	nc.Publish(protocol.SubjectCommands("linear-agent"), cmdData)
	nc.Flush()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mock.mu.Lock()
		n := len(mock.commandCalls)
		mock.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()

	if len(mock.commandCalls) == 0 {
		t.Fatal("no Linear API calls received; expected create_issue")
	}

	call := mock.commandCalls[0]
	if call.Method != "CreateIssue" {
		t.Errorf("method = %s, want CreateIssue", call.Method)
	}

	_ = la // keep reference
}

// --- Test helpers ---

type mockCommandCall struct {
	Method string
	Args   map[string]string
}

type mockLinearClient struct {
	mu           sync.Mutex
	issues       []linearagent.LinearIssue
	comments     []linearagent.LinearComment
	commandCalls []mockCommandCall
	pollCount    int
}

func (m *mockLinearClient) FetchUpdatedIssues(_ context.Context, _ time.Time, _ string) ([]linearagent.LinearIssue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	issues := m.issues
	m.issues = nil // only return on first poll
	m.pollCount++
	return issues, nil
}

func (m *mockLinearClient) FetchUpdatedComments(_ context.Context, _ time.Time) ([]linearagent.LinearComment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	comments := m.comments
	m.comments = nil
	return comments, nil
}

func (m *mockLinearClient) CreateIssue(_ context.Context, teamID, title, description string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commandCalls = append(m.commandCalls, mockCommandCall{
		Method: "CreateIssue",
		Args:   map[string]string{"team_id": teamID, "title": title, "description": description},
	})
	return "new-issue-id", nil
}

func (m *mockLinearClient) UpdateIssue(_ context.Context, issueID string, input map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commandCalls = append(m.commandCalls, mockCommandCall{
		Method: "UpdateIssue",
		Args:   map[string]string{"issue_id": issueID},
	})
	return nil
}

func (m *mockLinearClient) CreateComment(_ context.Context, issueID, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commandCalls = append(m.commandCalls, mockCommandCall{
		Method: "CreateComment",
		Args:   map[string]string{"issue_id": issueID, "body": body},
	})
	return nil
}

func (m *mockLinearClient) AddLabel(_ context.Context, issueID, labelID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commandCalls = append(m.commandCalls, mockCommandCall{
		Method: "AddLabel",
		Args:   map[string]string{"issue_id": issueID, "label_id": labelID},
	})
	return nil
}

func newTestDaemon(t *testing.T, wfDir string) (*server.Daemon, any) {
	t.Helper()
	tmpDir := t.TempDir()
	socketDir, err := os.MkdirTemp("/tmp", "sekia-test-*")
	if err != nil {
		t.Fatalf("create socket dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(socketDir) })
	socketPath := filepath.Join(socketDir, "s.sock")

	cfg := server.Config{
		Server: server.ServerConfig{Socket: socketPath},
		NATS:   server.NATSConfig{Embedded: true, DataDir: filepath.Join(tmpDir, "nats")},
	}
	if wfDir != "" {
		cfg.Workflows = server.WorkflowConfig{Dir: wfDir, HotReload: false}
	}

	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).
		With().Timestamp().Logger()

	d := server.NewDaemon(cfg, logger)

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run() }()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := os.Stat(socketPath); err != nil {
		t.Fatal("socket did not appear in time")
	}

	t.Cleanup(func() {
		d.Stop()
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("daemon error on shutdown: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("daemon did not shut down in time")
		}
	})

	return d, nil
}

func newTestLinearAgent(t *testing.T, d *server.Daemon, mock linearagent.LinearClient) *linearagent.LinearAgent {
	t.Helper()

	la := linearagent.NewTestAgent(
		d.NATSClientURL(),
		d.NATSConnectOpts(),
		mock,
		200*time.Millisecond, // fast poll for tests
		zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger(),
	)

	errCh := make(chan error, 1)
	go func() { errCh <- la.Run() }()

	time.Sleep(500 * time.Millisecond)

	t.Cleanup(func() {
		la.Stop()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Error("linear agent did not shut down in time")
		}
	})

	return la
}
