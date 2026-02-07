package server_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"github.com/sekia-ai/sekia/internal/server"
	"github.com/sekia-ai/sekia/pkg/agent"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

func TestEndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "sekiad.sock")

	cfg := server.Config{
		Server: server.ServerConfig{
			Socket: socketPath,
		},
		NATS: server.NATSConfig{
			Embedded: true,
			DataDir:  filepath.Join(tmpDir, "nats"),
		},
	}

	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).
		With().Timestamp().Logger()

	d := server.NewDaemon(cfg, logger)

	// Run daemon in background.
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run() }()

	// Wait for daemon to be ready.
	select {
	case <-d.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not start in time")
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		},
	}

	// Check status with no agents.
	resp, err := client.Get("http://sekiad/api/v1/status")
	if err != nil {
		t.Fatalf("status request: %v", err)
	}
	var status protocol.StatusResponse
	json.NewDecoder(resp.Body).Decode(&status)
	resp.Body.Close()

	if status.Status != "ok" {
		t.Fatalf("expected status ok, got %s", status.Status)
	}
	if status.AgentCount != 0 {
		t.Fatalf("expected 0 agents, got %d", status.AgentCount)
	}

	// Connect a test agent.
	testAgent, err := agent.New(agent.Config{
		NATSUrl:  d.NATSClientURL(),
		NATSOpts: d.NATSConnectOpts(),
	}, "test-agent", "0.1.0", []string{"testing"}, []string{"ping"}, logger)
	if err != nil {
		t.Fatalf("create test agent: %v", err)
	}
	defer testAgent.Close()

	// Give NATS a moment to deliver the registration + initial heartbeat.
	time.Sleep(500 * time.Millisecond)

	// Check agents list.
	resp, err = client.Get("http://sekiad/api/v1/agents")
	if err != nil {
		t.Fatalf("agents request: %v", err)
	}
	var agents protocol.AgentsResponse
	json.NewDecoder(resp.Body).Decode(&agents)
	resp.Body.Close()

	if len(agents.Agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents.Agents))
	}
	if agents.Agents[0].Name != "test-agent" {
		t.Fatalf("expected agent name test-agent, got %s", agents.Agents[0].Name)
	}
	if agents.Agents[0].Status != "running" {
		t.Fatalf("expected agent status running, got %s", agents.Agents[0].Status)
	}
	if agents.Agents[0].Version != "0.1.0" {
		t.Fatalf("expected agent version 0.1.0, got %s", agents.Agents[0].Version)
	}

	// Stop daemon.
	d.Stop()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("daemon error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not shut down in time")
	}
}

// newTestDaemon creates a daemon with a temp dir and optional workflow directory.
// Returns the daemon, HTTP client, and a cleanup function that stops the daemon.
func newTestDaemon(t *testing.T, wfDir string) (*server.Daemon, *http.Client) {
	t.Helper()
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "sekiad.sock")

	cfg := server.Config{
		Server: server.ServerConfig{Socket: socketPath},
		NATS:   server.NATSConfig{Embedded: true, DataDir: filepath.Join(tmpDir, "nats")},
		Workflows: server.WorkflowConfig{
			Dir:       wfDir,
			HotReload: false,
		},
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

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
			},
		},
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

	return d, client
}

func TestWorkflowEndToEnd(t *testing.T) {
	wfDir := t.TempDir()

	// Write a workflow that listens for test events and sends a command back.
	workflowCode := `
sekia.on("sekia.events.test", function(event)
	sekia.command("test-agent", "handle", {
		original_id = event.id,
		event_type  = event.type,
		title       = event.payload.title,
	})
end)
`
	os.WriteFile(filepath.Join(wfDir, "responder.lua"), []byte(workflowCode), 0644)

	d, client := newTestDaemon(t, wfDir)

	// Connect a test agent that listens for commands.
	testAgent, err := agent.New(agent.Config{
		NATSUrl:  d.NATSClientURL(),
		NATSOpts: d.NATSConnectOpts(),
	}, "test-agent", "0.1.0", []string{"testing"}, []string{"handle"}, zerolog.New(os.Stderr))
	if err != nil {
		t.Fatalf("create test agent: %v", err)
	}
	defer testAgent.Close()

	// Subscribe to the test agent's command subject to capture the workflow output.
	commandReceived := make(chan []byte, 1)
	sub, err := testAgent.Conn().Subscribe("sekia.commands.test-agent", func(msg *nats.Msg) {
		commandReceived <- msg.Data
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	// Give NATS time to process agent registration.
	time.Sleep(500 * time.Millisecond)

	// Publish a test event.
	ev := protocol.NewEvent("issue.opened", "test-source", map[string]any{
		"title":  "Fix the bug",
		"number": float64(42),
	})
	evData, _ := json.Marshal(ev)
	testAgent.Conn().Publish("sekia.events.test", evData)
	testAgent.Conn().Flush()

	// Wait for the workflow to process the event and send a command.
	select {
	case cmdData := <-commandReceived:
		var cmd map[string]any
		json.Unmarshal(cmdData, &cmd)
		if cmd["command"] != "handle" {
			t.Errorf("command = %v, want handle", cmd["command"])
		}
		if cmd["source"] != "workflow:responder" {
			t.Errorf("source = %v, want workflow:responder", cmd["source"])
		}
		payload := cmd["payload"].(map[string]any)
		if payload["original_id"] != ev.ID {
			t.Errorf("original_id = %v, want %s", payload["original_id"], ev.ID)
		}
		if payload["title"] != "Fix the bug" {
			t.Errorf("title = %v, want Fix the bug", payload["title"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for workflow command")
	}

	// Verify workflows API endpoint.
	resp, err := client.Get("http://sekiad/api/v1/workflows")
	if err != nil {
		t.Fatalf("workflows request: %v", err)
	}
	var wfResp protocol.WorkflowsResponse
	json.NewDecoder(resp.Body).Decode(&wfResp)
	resp.Body.Close()

	if len(wfResp.Workflows) != 1 {
		t.Fatalf("expected 1 workflow, got %d", len(wfResp.Workflows))
	}
	if wfResp.Workflows[0].Name != "responder" {
		t.Errorf("workflow name = %s, want responder", wfResp.Workflows[0].Name)
	}
	if wfResp.Workflows[0].Handlers != 1 {
		t.Errorf("handlers = %d, want 1", wfResp.Workflows[0].Handlers)
	}

	// Verify status includes workflow count.
	resp, err = client.Get("http://sekiad/api/v1/status")
	if err != nil {
		t.Fatalf("status request: %v", err)
	}
	var status protocol.StatusResponse
	json.NewDecoder(resp.Body).Decode(&status)
	resp.Body.Close()

	if status.WorkflowCount != 1 {
		t.Errorf("workflow_count = %d, want 1", status.WorkflowCount)
	}
}
