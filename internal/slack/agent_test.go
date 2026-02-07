package slack_test

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

	slackagent "github.com/sekia-ai/sekia/internal/slack"
	"github.com/sekia-ai/sekia/internal/server"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

// TestSlackAgentEndToEnd tests the full flow:
//
//	slack event on NATS → Lua workflow → NATS command → slack-agent → mock Slack API
func TestSlackAgentEndToEnd(t *testing.T) {
	// 1. Create a mock Slack client that records API calls.
	mock := &mockSlackClient{}

	// 2. Write a Lua workflow that auto-replies to mentions.
	wfDir := t.TempDir()
	workflowCode := `
sekia.on("sekia.events.slack", function(event)
	if event.type ~= "slack.message.received" then return end
	sekia.command("slack-agent", "send_message", {
		channel = event.payload.channel,
		text    = "echo: " .. event.payload.text,
	})
end)
`
	os.WriteFile(filepath.Join(wfDir, "auto-echo.lua"), []byte(workflowCode), 0644)

	// 3. Start sekiad daemon with workflow engine.
	d, _ := newTestDaemon(t, wfDir)

	// 4. Start the Slack agent connecting to the daemon's in-process NATS.
	sa := newTestSlackAgent(t, d, mock)

	// Give everything time to wire up.
	time.Sleep(800 * time.Millisecond)

	// 5. Publish a fake Slack event directly to NATS (bypassing Socket Mode).
	nc, err := nats.Connect(d.NATSClientURL(), d.NATSConnectOpts()...)
	if err != nil {
		t.Fatalf("connect nats: %v", err)
	}
	defer nc.Drain()

	ev := protocol.NewEvent("slack.message.received", "slack", map[string]any{
		"channel":   "C12345",
		"user":      "U67890",
		"text":      "hello world",
		"timestamp": "1234567890.123456",
	})
	evData, _ := json.Marshal(ev)
	nc.Publish(protocol.SubjectEvents("slack"), evData)
	nc.Flush()

	// 6. Wait for the command to propagate through the workflow engine.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mock.mu.Lock()
		n := len(mock.calls)
		mock.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()

	if len(mock.calls) == 0 {
		t.Fatal("no Slack API calls received; expected send_message")
	}

	call := mock.calls[0]
	if call.Method != "PostMessage" {
		t.Errorf("method = %s, want PostMessage", call.Method)
	}
	if call.Args["channel"] != "C12345" {
		t.Errorf("channel = %s, want C12345", call.Args["channel"])
	}
	if call.Args["text"] != "echo: hello world" {
		t.Errorf("text = %s, want 'echo: hello world'", call.Args["text"])
	}

	_ = sa // keep reference
}

// TestSlackAgentCommandHandling tests that commands received via NATS
// are correctly dispatched to the Slack API.
func TestSlackAgentCommandHandling(t *testing.T) {
	mock := &mockSlackClient{}
	d, _ := newTestDaemon(t, "")
	sa := newTestSlackAgent(t, d, mock)

	time.Sleep(800 * time.Millisecond)

	// Send commands directly via NATS.
	nc, err := nats.Connect(d.NATSClientURL(), d.NATSConnectOpts()...)
	if err != nil {
		t.Fatalf("connect nats: %v", err)
	}
	defer nc.Drain()

	tests := []struct {
		name    string
		command string
		payload map[string]any
		method  string
	}{
		{
			name:    "send_message",
			command: "send_message",
			payload: map[string]any{"channel": "C111", "text": "hello"},
			method:  "PostMessage",
		},
		{
			name:    "send_reply",
			command: "send_reply",
			payload: map[string]any{"channel": "C222", "thread_ts": "111.222", "text": "reply"},
			method:  "PostReply",
		},
		{
			name:    "add_reaction",
			command: "add_reaction",
			payload: map[string]any{"channel": "C333", "timestamp": "111.333", "emoji": "thumbsup"},
			method:  "AddReaction",
		},
	}

	for _, tt := range tests {
		cmd := map[string]any{
			"command": tt.command,
			"payload": tt.payload,
			"source":  "test",
		}
		cmdData, _ := json.Marshal(cmd)
		nc.Publish(protocol.SubjectCommands("slack-agent"), cmdData)
	}
	nc.Flush()

	// Wait for all commands to be processed.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mock.mu.Lock()
		n := len(mock.calls)
		mock.mu.Unlock()
		if n >= len(tests) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()

	if len(mock.calls) < len(tests) {
		t.Fatalf("received %d API calls, want %d", len(mock.calls), len(tests))
	}

	// Check each call was dispatched to the right method.
	methods := make(map[string]bool)
	for _, c := range mock.calls {
		methods[c.Method] = true
	}
	for _, tt := range tests {
		if !methods[tt.method] {
			t.Errorf("missing API call for method %s", tt.method)
		}
	}

	_ = sa // keep reference
}

// --- Test helpers ---

type mockCall struct {
	Method string
	Args   map[string]string
}

type mockSlackClient struct {
	mu    sync.Mutex
	calls []mockCall
}

func (m *mockSlackClient) PostMessage(_ context.Context, channel, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, mockCall{"PostMessage", map[string]string{"channel": channel, "text": text}})
	return nil
}

func (m *mockSlackClient) PostReply(_ context.Context, channel, threadTS, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, mockCall{"PostReply", map[string]string{"channel": channel, "thread_ts": threadTS, "text": text}})
	return nil
}

func (m *mockSlackClient) AddReaction(_ context.Context, channel, timestamp, emoji string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, mockCall{"AddReaction", map[string]string{"channel": channel, "timestamp": timestamp, "emoji": emoji}})
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

func newTestSlackAgent(t *testing.T, d *server.Daemon, mock slackagent.SlackClient) *slackagent.SlackAgent {
	t.Helper()

	sa := slackagent.NewTestAgent(
		d.NATSClientURL(),
		d.NATSConnectOpts(),
		mock,
		zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger(),
	)

	errCh := make(chan error, 1)
	go func() { errCh <- sa.Run() }()

	// Give the agent time to connect to NATS and subscribe.
	time.Sleep(500 * time.Millisecond)

	t.Cleanup(func() {
		sa.Stop()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Error("slack agent did not shut down in time")
		}
	})

	return sa
}
