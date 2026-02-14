package google_test

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

	googleagent "github.com/sekia-ai/sekia/internal/google"
	"github.com/sekia-ai/sekia/internal/server"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

// TestGoogleAgentGmailEndToEnd tests the full Gmail flow:
// poller fetches emails → event on NATS → Lua workflow → command → mock Gmail API
func TestGoogleAgentGmailEndToEnd(t *testing.T) {
	mock := &mockGmailClient{
		profileEmail:     "bot@example.com",
		profileHistoryID: 12345,
		initialMessages: []googleagent.EmailMessage{
			{
				ID:        "msg-001",
				ThreadID:  "thread-001",
				MessageID: "<msg-1@example.com>",
				From:      "alice@example.com",
				To:        "bot@example.com",
				Subject:   "Urgent: server down",
				Body:      "The server is down, please check.",
				Date:      time.Now().Format(time.RFC3339),
				Labels:    []string{"INBOX", "UNREAD"},
			},
		},
	}

	wfDir := t.TempDir()
	workflowCode := `
sekia.on("sekia.events.google", function(event)
	if event.type ~= "gmail.message.received" then return end
	local subject = string.lower(event.payload.subject or "")
	if string.find(subject, "urgent") then
		sekia.command("google-agent", "send_email", {
			to      = event.payload.from,
			subject = "Re: " .. event.payload.subject,
			body    = "auto-reply: acknowledged",
		})
	end
end)
`
	os.WriteFile(filepath.Join(wfDir, "auto-reply.lua"), []byte(workflowCode), 0644)

	d, _ := newTestDaemon(t, wfDir)
	ga := newTestGoogleAgent(t, d, mock, nil)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mock.mu.Lock()
		n := len(mock.gmailCalls)
		mock.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	mock.mu.Lock()
	defer mock.mu.Unlock()

	if len(mock.gmailCalls) == 0 {
		t.Fatal("no Gmail API command calls received; expected send_email")
	}

	call := mock.gmailCalls[0]
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

// TestGoogleAgentCalendarEndToEnd tests the full Calendar flow:
// poller fetches events → event on NATS → Lua workflow → command → mock Calendar API
func TestGoogleAgentCalendarEndToEnd(t *testing.T) {
	calMock := &mockCalendarClient{
		events: []googleagent.CalendarEvent{
			{
				ID:      "evt-001",
				Summary: "Team standup",
				Start:   time.Now().Add(1 * time.Hour),
				End:     time.Now().Add(2 * time.Hour),
				Status:  "confirmed",
				Created: time.Now(), // recently created → will emit "created" event
			},
		},
		syncToken: "sync-token-1",
	}

	wfDir := t.TempDir()
	workflowCode := `
sekia.on("sekia.events.google", function(event)
	if event.type ~= "google.calendar.event.created" then return end
	sekia.command("google-agent", "create_event", {
		summary = "Follow-up: " .. (event.payload.summary or ""),
		start   = "2026-03-01T10:00:00Z",
		["end"] = "2026-03-01T11:00:00Z",
	})
end)
`
	os.WriteFile(filepath.Join(wfDir, "calendar-followup.lua"), []byte(workflowCode), 0644)

	d, _ := newTestDaemon(t, wfDir)
	ga := newTestGoogleAgent(t, d, nil, calMock)

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		calMock.mu.Lock()
		n := len(calMock.calendarCalls)
		calMock.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	calMock.mu.Lock()
	defer calMock.mu.Unlock()

	if len(calMock.calendarCalls) == 0 {
		t.Fatal("no Calendar API command calls received; expected create_event")
	}

	call := calMock.calendarCalls[0]
	if call.Method != "CreateEvent" {
		t.Errorf("method = %s, want CreateEvent", call.Method)
	}
	if call.Args["summary"] != "Follow-up: Team standup" {
		t.Errorf("summary = %s, want 'Follow-up: Team standup'", call.Args["summary"])
	}

	_ = ga // keep reference
}

// TestGoogleAgentCommandHandling tests that commands received via NATS
// are correctly dispatched to the appropriate API.
func TestGoogleAgentCommandHandling(t *testing.T) {
	gmailMock := &mockGmailClient{
		profileEmail:     "bot@example.com",
		profileHistoryID: 12345,
	}
	calMock := &mockCalendarClient{syncToken: "sync-token-1"}

	d, _ := newTestDaemon(t, "")
	ga := newTestGoogleAgent(t, d, gmailMock, calMock)

	time.Sleep(800 * time.Millisecond)

	nc, err := nats.Connect(d.NATSClientURL(), d.NATSConnectOpts()...)
	if err != nil {
		t.Fatalf("connect nats: %v", err)
	}
	defer nc.Drain()

	// Send a Gmail send_email command.
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
	nc.Publish(protocol.SubjectCommands("google-agent"), cmdData)
	nc.Flush()

	waitForCalls(t, &gmailMock.mu, &gmailMock.gmailCalls, 1)

	gmailMock.mu.Lock()
	call := gmailMock.gmailCalls[0]
	gmailMock.mu.Unlock()

	if call.Method != "SendEmail" {
		t.Errorf("method = %s, want SendEmail", call.Method)
	}
	if call.Args["to"] != "bob@example.com" {
		t.Errorf("to = %s, want bob@example.com", call.Args["to"])
	}

	// Send a Calendar create_event command.
	cmd2 := map[string]any{
		"command": "create_event",
		"payload": map[string]any{
			"summary": "Meeting",
			"start":   "2026-03-01T10:00:00Z",
			"end":     "2026-03-01T11:00:00Z",
		},
		"source": "test",
	}
	cmdData2, _ := json.Marshal(cmd2)
	nc.Publish(protocol.SubjectCommands("google-agent"), cmdData2)
	nc.Flush()

	waitForCalls(t, &calMock.mu, &calMock.calendarCalls, 1)

	calMock.mu.Lock()
	calCall := calMock.calendarCalls[0]
	calMock.mu.Unlock()

	if calCall.Method != "CreateEvent" {
		t.Errorf("method = %s, want CreateEvent", calCall.Method)
	}
	if calCall.Args["summary"] != "Meeting" {
		t.Errorf("summary = %s, want Meeting", calCall.Args["summary"])
	}

	_ = ga // keep reference
}

// --- Test helpers ---

type mockCommandCall struct {
	Method string
	Args   map[string]string
}

func waitForCalls(t *testing.T, mu *sync.Mutex, calls *[]mockCommandCall, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(*calls)
		mu.Unlock()
		if n >= want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*calls) < want {
		t.Fatalf("expected at least %d calls, got %d", want, len(*calls))
	}
}

// --- Mock Gmail client ---

type mockGmailClient struct {
	mu               sync.Mutex
	profileEmail     string
	profileHistoryID uint64
	initialMessages  []googleagent.EmailMessage
	historyMessages  []googleagent.EmailMessage
	gmailCalls       []mockCommandCall
	seeded           bool
}

func (m *mockGmailClient) GetProfile(_ context.Context, _ string) (string, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.profileEmail, m.profileHistoryID, nil
}

func (m *mockGmailClient) ListHistory(_ context.Context, _ string, _ uint64) ([]googleagent.EmailMessage, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	msgs := m.historyMessages
	m.historyMessages = nil
	return msgs, m.profileHistoryID, nil
}

func (m *mockGmailClient) ListMessages(_ context.Context, _ string, _ string, _ int64) ([]googleagent.EmailMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	msgs := m.initialMessages
	m.initialMessages = nil
	return msgs, nil
}

func (m *mockGmailClient) SendEmail(_ context.Context, _, to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gmailCalls = append(m.gmailCalls, mockCommandCall{
		Method: "SendEmail",
		Args:   map[string]string{"to": to, "subject": subject, "body": body},
	})
	return nil
}

func (m *mockGmailClient) ReplyEmail(_ context.Context, _, threadID, inReplyTo, to, subject, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gmailCalls = append(m.gmailCalls, mockCommandCall{
		Method: "ReplyEmail",
		Args:   map[string]string{"thread_id": threadID, "to": to, "subject": subject, "body": body, "in_reply_to": inReplyTo},
	})
	return nil
}

func (m *mockGmailClient) AddLabel(_ context.Context, _, messageID, label string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gmailCalls = append(m.gmailCalls, mockCommandCall{
		Method: "AddLabel",
		Args:   map[string]string{"message_id": messageID, "label": label},
	})
	return nil
}

func (m *mockGmailClient) RemoveLabel(_ context.Context, _, messageID, label string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gmailCalls = append(m.gmailCalls, mockCommandCall{
		Method: "RemoveLabel",
		Args:   map[string]string{"message_id": messageID, "label": label},
	})
	return nil
}

func (m *mockGmailClient) Archive(_ context.Context, _, messageID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gmailCalls = append(m.gmailCalls, mockCommandCall{
		Method: "Archive",
		Args:   map[string]string{"message_id": messageID},
	})
	return nil
}

func (m *mockGmailClient) Trash(_ context.Context, _, messageID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gmailCalls = append(m.gmailCalls, mockCommandCall{
		Method: "Trash",
		Args:   map[string]string{"message_id": messageID},
	})
	return nil
}

func (m *mockGmailClient) Delete(_ context.Context, _, messageID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gmailCalls = append(m.gmailCalls, mockCommandCall{
		Method: "Delete",
		Args:   map[string]string{"message_id": messageID},
	})
	return nil
}

// --- Mock Calendar client ---

type mockCalendarClient struct {
	mu            sync.Mutex
	events        []googleagent.CalendarEvent
	syncToken     string
	calendarCalls []mockCommandCall
}

func (m *mockCalendarClient) ListEvents(_ context.Context, _ string, syncToken string, _ time.Time) ([]googleagent.CalendarEvent, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Return events only on first sync (no syncToken).
	if syncToken == "" {
		evts := m.events
		m.events = nil
		return evts, m.syncToken, nil
	}
	return nil, m.syncToken, nil
}

func (m *mockCalendarClient) ListUpcomingEvents(_ context.Context, _ string, _ int) ([]googleagent.CalendarEvent, error) {
	return nil, nil
}

func (m *mockCalendarClient) CreateEvent(_ context.Context, _ string, event googleagent.CalendarEvent) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calendarCalls = append(m.calendarCalls, mockCommandCall{
		Method: "CreateEvent",
		Args:   map[string]string{"summary": event.Summary},
	})
	return "new-event-id", nil
}

func (m *mockCalendarClient) UpdateEvent(_ context.Context, _, eventID string, updates map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calendarCalls = append(m.calendarCalls, mockCommandCall{
		Method: "UpdateEvent",
		Args:   map[string]string{"event_id": eventID},
	})
	return nil
}

func (m *mockCalendarClient) DeleteEvent(_ context.Context, _, eventID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calendarCalls = append(m.calendarCalls, mockCommandCall{
		Method: "DeleteEvent",
		Args:   map[string]string{"event_id": eventID},
	})
	return nil
}

// --- Daemon + agent helpers ---

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

func newTestGoogleAgent(t *testing.T, d *server.Daemon, gmailMock googleagent.GmailClient, calMock googleagent.CalendarClient) *googleagent.GoogleAgent {
	t.Helper()

	ga := googleagent.NewTestAgent(
		d.NATSClientURL(),
		d.NATSConnectOpts(),
		gmailMock,
		calMock,
		200*time.Millisecond, // fast poll for tests
		200*time.Millisecond,
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
			t.Error("google agent did not shut down in time")
		}
	})

	return ga
}
