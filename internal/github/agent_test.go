package github_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"

	ghagent "github.com/sekia-ai/sekia/internal/github"
	"github.com/sekia-ai/sekia/internal/server"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

// TestGitHubAgentEndToEnd tests the full flow:
//
//	GitHub webhook → github-agent → NATS event → Lua workflow → NATS command → github-agent → GitHub API
func TestGitHubAgentEndToEnd(t *testing.T) {
	// 1. Set up a mock GitHub API server that records incoming calls.
	var mu sync.Mutex
	var apiCalls []apiCall

	mockGH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 0)
		if r.Body != nil {
			buf := new(bytes.Buffer)
			buf.ReadFrom(r.Body)
			body = buf.Bytes()
		}

		mu.Lock()
		apiCalls = append(apiCalls, apiCall{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   string(body),
		})
		mu.Unlock()

		// Return a valid response for AddLabelsToIssue.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[{"name":"triage"}]`)
	}))
	defer mockGH.Close()

	// 2. Write a Lua workflow that auto-labels issues.
	wfDir := t.TempDir()
	workflowCode := `
sekia.on("sekia.events.github", function(event)
	if event.type ~= "github.issue.opened" then return end
	sekia.command("github-agent", "add_label", {
		owner  = event.payload.owner,
		repo   = event.payload.repo,
		number = event.payload.number,
		label  = "triage",
	})
end)
`
	os.WriteFile(filepath.Join(wfDir, "auto-label.lua"), []byte(workflowCode), 0644)

	// 3. Start sekiad daemon with workflow engine.
	d, _ := newTestDaemon(t, wfDir)

	// 4. Start the GitHub agent connecting to the daemon's in-process NATS.
	ga := newTestGitHubAgent(t, d, mockGH.URL)

	// Give everything time to wire up.
	time.Sleep(800 * time.Millisecond)

	// 5. POST a fake webhook to the agent's webhook endpoint.
	webhookPayload, _ := json.Marshal(map[string]any{
		"action": "opened",
		"issue": map[string]any{
			"number":   42,
			"title":    "Bug: crash on startup",
			"body":     "It crashes",
			"html_url": "https://github.com/myorg/myrepo/issues/42",
			"user":     map[string]any{"login": "alice"},
			"labels":   []any{},
		},
		"repository": map[string]any{
			"name":  "myrepo",
			"owner": map[string]any{"login": "myorg"},
		},
	})

	webhookURL := fmt.Sprintf("http://%s/webhook", ga.WebhookAddr)
	req, _ := http.NewRequest("POST", webhookURL, bytes.NewReader(webhookPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", "issues")
	req.Header.Set("X-GitHub-Delivery", "test-delivery-1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post webhook: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("webhook status = %d, want 200", resp.StatusCode)
	}

	// 6. Wait for the command to propagate through the workflow engine and back to the agent.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(apiCalls)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(apiCalls) == 0 {
		t.Fatal("no GitHub API calls received; expected add_label call")
	}

	call := apiCalls[0]
	expectedPath := "/repos/myorg/myrepo/issues/42/labels"
	if call.Path != expectedPath {
		t.Errorf("API path = %s, want %s", call.Path, expectedPath)
	}
	if call.Method != "POST" {
		t.Errorf("API method = %s, want POST", call.Method)
	}
}

// TestGitHubAgentCommandHandling tests that commands received via NATS
// are correctly dispatched to the GitHub API.
func TestGitHubAgentCommandHandling(t *testing.T) {
	var mu sync.Mutex
	var apiCalls []apiCall

	mockGH := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := new(bytes.Buffer)
		body.ReadFrom(r.Body)

		mu.Lock()
		apiCalls = append(apiCalls, apiCall{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   body.String(),
		})
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	}))
	defer mockGH.Close()

	d, _ := newTestDaemon(t, "")
	ga := newTestGitHubAgent(t, d, mockGH.URL)

	time.Sleep(800 * time.Millisecond)

	// Send a create_comment command directly via NATS.
	cmd := map[string]any{
		"command": "create_comment",
		"payload": map[string]any{
			"owner":  "testorg",
			"repo":   "testrepo",
			"number": float64(7),
			"body":   "Automated comment from sekia",
		},
		"source": "test",
	}
	cmdData, _ := json.Marshal(cmd)

	// Connect a separate NATS client to publish the command.
	nc, err := nats.Connect(d.NATSClientURL(), d.NATSConnectOpts()...)
	if err != nil {
		t.Fatalf("connect nats: %v", err)
	}
	defer nc.Drain()

	nc.Publish(protocol.SubjectCommands("github-agent"), cmdData)
	nc.Flush()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(apiCalls)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(apiCalls) == 0 {
		t.Fatal("no GitHub API calls received; expected create_comment")
	}

	call := apiCalls[0]
	if call.Path != "/repos/testorg/testrepo/issues/7/comments" {
		t.Errorf("API path = %s, want /repos/testorg/testrepo/issues/7/comments", call.Path)
	}

	_ = ga // keep reference
}

// --- Test helpers ---

type apiCall struct {
	Method string
	Path   string
	Body   string
}

type testGitHubAgent struct {
	*ghagent.GitHubAgent
	WebhookAddr string
}

func newTestDaemon(t *testing.T, wfDir string) (*server.Daemon, *http.Client) {
	t.Helper()
	tmpDir := t.TempDir()
	// Use a short socket path to stay under macOS's 104-char Unix socket limit.
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

	select {
	case <-d.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not start in time")
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

func newTestGitHubAgent(t *testing.T, d *server.Daemon, mockGHURL string) *testGitHubAgent {
	t.Helper()

	ga := ghagent.NewTestAgent(
		d.NATSClientURL(),
		d.NATSConnectOpts(),
		mockGHURL,
		":0", // random port
		zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger(),
	)

	errCh := make(chan error, 1)
	go func() { errCh <- ga.Run() }()

	select {
	case <-ga.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("github agent did not start in time")
	}

	t.Cleanup(func() {
		ga.Stop()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Error("github agent did not shut down in time")
		}
	})

	return &testGitHubAgent{GitHubAgent: ga, WebhookAddr: ga.WebhookAddr()}
}
