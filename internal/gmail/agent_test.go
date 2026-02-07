package gmail_test

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

	gmailagent "github.com/sekia-ai/sekia/internal/gmail"
	"github.com/sekia-ai/sekia/internal/server"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

// TestGmailAgentEndToEnd tests the full flow:
//
//	poller fetches emails → event on NATS → Lua workflow → command → mock Gmail API
func TestGmailAgentEndToEnd(t *testing.T) {
	// 1. Create a mock Gmail client that returns a fake email and records commands.
	mock := &mockGmailClient{
		messages: []gmailagent.EmailMessage{
			{
				UID:       100,
				MessageID: "<msg-1@example.com>",
				From:      "alice@example.com",
				To:        "bot@example.com",
				Subject:   "Urgent: server down",
				Body:      "The server is down, please check.",
				Date:      time.Now().Format(time.RFC3339),
			},
		},
	}

	// 2. Write a Lua workflow that auto-replies to urgent emails.
	wfDir := t.TempDir()
	workflowCode := `
sekia.on("sekia.events.gmail", function(event)
	if event.type ~= "gmail.message.received" then return end
	local subject = string.lower(event.payload.subject or "")
	if string.find(subject, "urgent") then
		sekia.command("gmail-agent", "send_email", {
			to      = event.payload.from,
			subject = "Re: " .. event.payload.subject,
			body    = "auto-reply: acknowledged",
		})
	end
end)
`
	os.WriteFile(filepath.Join(wfDir, "auto-reply.lua"), []byte(workflowCode), 0644)

	// 3. Start sekiad daemon with workflow engine.
	d, _ := newTestDaemon(t, wfDir)

	// 4. Start the Gmail agent with a fast poll interval.
	ga := newTestGmailAgent(t, d, mock)

	// 5. Wait for the poller to fetch the email, publish event, and workflow to dispatch command.
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
		t.Fatal("no Gmail API command calls received; expected send_email")
	}

	call := mock.commandCalls[0]
	if call.Method != "SendEmail" {
		t.Errorf("method = %s, want SendEmail", call.Method)
	}
	if call.Args["to"] != "alice@example.com" {
		t.Errorf("to = %s, want alice@example.com", call.Args["to"])
	}
	if call.Args["body"] != "auto-reply: acknowledged" {
		t.Errorf("body = %s, want 'auto-reply: acknowledged'", call.Args["body"])
	}

	_ = ga // keep reference
}

// TestGmailAgentCommandHandling tests that commands received via NATS
// are correctly dispatched to the Gmail API.
func TestGmailAgentCommandHandling(t *testing.T) {
	mock := &mockGmailClient{}
	d, _ := newTestDaemon(t, "")
	ga := newTestGmailAgent(t, d, mock)

	time.Sleep(800 * time.Millisecond)

	nc, err := nats.Connect(d.NATSClientURL(), d.NATSConnectOpts()...)
	if err != nil {
		t.Fatalf("connect nats: %v", err)
	}
	defer nc.Drain()

	// Send a send_email command.
	cmd := map[string]any{
		"command": "send_email",
		"payload": map[string]any{
			"to":      "bob@example.com",
			"subject": "Hello",
			"body":    "Test body",
		},
		"source": "test",
	}
	cmdData, _ := json.Marshal(cmd)
	nc.Publish(protocol.SubjectCommands("gmail-agent"), cmdData)
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
		t.Fatal("no Gmail API calls received; expected send_email")
	}

	call := mock.commandCalls[0]
	if call.Method != "SendEmail" {
		t.Errorf("method = %s, want SendEmail", call.Method)
	}
	if call.Args["to"] != "bob@example.com" {
		t.Errorf("to = %s, want bob@example.com", call.Args["to"])
	}

	_ = ga // keep reference
}

// --- Test helpers ---

type mockCommandCall struct {
	Method string
	Args   map[string]string
}

type mockGmailClient struct {
	mu           sync.Mutex
	messages     []gmailagent.EmailMessage
	commandCalls []mockCommandCall
	pollCount    int
}

func (m *mockGmailClient) FetchNewMessages(_ context.Context, _ string, _ uint32) ([]gmailagent.EmailMessage, uint32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	msgs := m.messages
	m.messages = nil // only return on first poll
	m.pollCount++
	var highest uint32
	for _, msg := range msgs {
		if msg.UID > highest {
			highest = msg.UID
		}
	}
	return msgs, highest, nil
}

func (m *mockGmailClient) SendEmail(_ context.Context, to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commandCalls = append(m.commandCalls, mockCommandCall{
		Method: "SendEmail",
		Args:   map[string]string{"to": to, "subject": subject, "body": body},
	})
	return nil
}

func (m *mockGmailClient) ReplyEmail(_ context.Context, messageID, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commandCalls = append(m.commandCalls, mockCommandCall{
		Method: "ReplyEmail",
		Args:   map[string]string{"message_id": messageID, "body": body},
	})
	return nil
}

func (m *mockGmailClient) AddLabel(_ context.Context, messageUID uint32, label string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commandCalls = append(m.commandCalls, mockCommandCall{
		Method: "AddLabel",
		Args:   map[string]string{"label": label},
	})
	return nil
}

func (m *mockGmailClient) Archive(_ context.Context, messageUID uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commandCalls = append(m.commandCalls, mockCommandCall{
		Method: "Archive",
		Args:   map[string]string{},
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

func newTestGmailAgent(t *testing.T, d *server.Daemon, mock gmailagent.GmailClient) *gmailagent.GmailAgent {
	t.Helper()

	ga := gmailagent.NewTestAgent(
		d.NATSClientURL(),
		d.NATSConnectOpts(),
		mock,
		200*time.Millisecond, // fast poll for tests
		zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger(),
	)

	errCh := make(chan error, 1)
	go func() { errCh <- ga.Run() }()

	time.Sleep(500 * time.Millisecond)

	t.Cleanup(func() {
		ga.Stop()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Error("gmail agent did not shut down in time")
		}
	})

	return ga
}
