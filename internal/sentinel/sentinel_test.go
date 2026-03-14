package sentinel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"

	"github.com/sekia-ai/sekia/internal/ai"
	"github.com/sekia-ai/sekia/internal/registry"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

func testLogger() zerolog.Logger {
	return zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()
}

// mockLLM implements ai.LLMClient for testing.
type mockLLM struct {
	mu       sync.Mutex
	response string
	err      error
	calls    []ai.CompleteRequest
}

func (m *mockLLM) Complete(_ context.Context, req ai.CompleteRequest) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, req)
	return m.response, m.err
}

func startTestNATS(t *testing.T) (*server.Server, *nats.Conn) {
	t.Helper()
	opts := &server.Options{
		Host:           "127.0.0.1",
		Port:           -1,
		NoLog:          true,
		NoSigs:         true,
		MaxControlLine: 4096,
	}
	ns, err := server.NewServer(opts)
	if err != nil {
		t.Fatalf("start test nats: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(2 * time.Second) {
		t.Fatal("nats not ready")
	}
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatalf("connect to test nats: %v", err)
	}
	t.Cleanup(func() {
		nc.Close()
		ns.Shutdown()
	})
	return ns, nc
}

func TestLoadChecklist_FileExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sentinel.md")
	content := "- Check PRs\n- Check tickets\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := loadChecklist(path)
	if err != nil {
		t.Fatalf("loadChecklist() error: %v", err)
	}
	if result != "- Check PRs\n- Check tickets" {
		t.Errorf("result = %q, want trimmed content", result)
	}
}

func TestLoadChecklist_FileNotExists(t *testing.T) {
	result, err := loadChecklist("/nonexistent/sentinel.md")
	if err != nil {
		t.Fatalf("loadChecklist() error: %v", err)
	}
	if result != "" {
		t.Errorf("result = %q, want empty", result)
	}
}

func TestLoadChecklist_EmptyPath(t *testing.T) {
	result, err := loadChecklist("")
	if err != nil {
		t.Fatalf("loadChecklist() error: %v", err)
	}
	if result != "" {
		t.Errorf("result = %q, want empty", result)
	}
}

func TestSentinel_TickPublishesActions(t *testing.T) {
	_, nc := startTestNATS(t)

	reg, err := registry.New(nc, testLogger())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	defer reg.Close()

	dir := t.TempDir()
	checklistPath := filepath.Join(dir, "sentinel.md")
	if err := os.WriteFile(checklistPath, []byte("- Are there stale PRs?"), 0644); err != nil {
		t.Fatal(err)
	}

	mock := &mockLLM{
		response: `{"actions":[{"type":"sentinel.action.required","reasoning":"3 PRs are stale","checklist_item":"Are there stale PRs?"}]}`,
	}

	s := New(Config{
		Enabled:       true,
		Interval:      time.Minute,
		ChecklistPath: checklistPath,
	}, mock, nc, reg, nil, testLogger())

	// Subscribe to sentinel events before tick.
	received := make(chan protocol.Event, 1)
	sub, err := nc.Subscribe("sekia.events.sentinel", func(msg *nats.Msg) {
		var evt protocol.Event
		json.Unmarshal(msg.Data, &evt)
		received <- evt
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()
	nc.Flush()

	s.tick(context.Background())

	select {
	case evt := <-received:
		if evt.Type != "sentinel.action.required" {
			t.Errorf("event type = %q, want %q", evt.Type, "sentinel.action.required")
		}
		if evt.Source != "sentinel" {
			t.Errorf("event source = %q, want %q", evt.Source, "sentinel")
		}
		if evt.Payload["reasoning"] != "3 PRs are stale" {
			t.Errorf("reasoning = %v, want %q", evt.Payload["reasoning"], "3 PRs are stale")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for sentinel event")
	}
}

func TestSentinel_TickNoActions(t *testing.T) {
	_, nc := startTestNATS(t)

	reg, err := registry.New(nc, testLogger())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	defer reg.Close()

	dir := t.TempDir()
	checklistPath := filepath.Join(dir, "sentinel.md")
	if err := os.WriteFile(checklistPath, []byte("- Check agents"), 0644); err != nil {
		t.Fatal(err)
	}

	mock := &mockLLM{
		response: `{"actions":[]}`,
	}

	s := New(Config{
		Enabled:       true,
		Interval:      time.Minute,
		ChecklistPath: checklistPath,
	}, mock, nc, reg, nil, testLogger())

	// Subscribe to sentinel events.
	received := make(chan struct{}, 1)
	sub, err := nc.Subscribe("sekia.events.sentinel", func(_ *nats.Msg) {
		received <- struct{}{}
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()
	nc.Flush()

	s.tick(context.Background())

	select {
	case <-received:
		t.Fatal("expected no events, but got one")
	case <-time.After(200 * time.Millisecond):
		// Expected: no events published.
	}
}

func TestSentinel_MissingChecklist(t *testing.T) {
	_, nc := startTestNATS(t)

	reg, err := registry.New(nc, testLogger())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	defer reg.Close()

	mock := &mockLLM{response: `{"actions":[]}`}

	s := New(Config{
		Enabled:       true,
		Interval:      time.Minute,
		ChecklistPath: "/nonexistent/sentinel.md",
	}, mock, nc, reg, nil, testLogger())

	// Should not panic or call LLM when checklist is missing.
	s.tick(context.Background())

	mock.mu.Lock()
	calls := len(mock.calls)
	mock.mu.Unlock()
	if calls != 0 {
		t.Errorf("expected 0 LLM calls with missing checklist, got %d", calls)
	}
}

func TestSentinel_StartStop(t *testing.T) {
	_, nc := startTestNATS(t)

	reg, err := registry.New(nc, testLogger())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	defer reg.Close()

	mock := &mockLLM{response: `{"actions":[]}`}

	s := New(Config{
		Enabled:       true,
		Interval:      time.Hour, // long interval so tick doesn't fire
		ChecklistPath: "",
	}, mock, nc, reg, nil, testLogger())

	s.Start()
	// Should not panic on double stop.
	s.Stop()
	s.Stop()
}
