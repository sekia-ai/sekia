package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"

	"github.com/sekia-ai/sekia/internal/server"
	"github.com/sekia-ai/sekia/pkg/agent"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

// mockAPI implements DaemonAPI for unit tests.
type mockAPI struct {
	status    *protocol.StatusResponse
	agents    *protocol.AgentsResponse
	workflows *protocol.WorkflowsResponse
	reloadErr error
}

func (m *mockAPI) GetStatus(_ context.Context) (*protocol.StatusResponse, error) {
	return m.status, nil
}
func (m *mockAPI) GetAgents(_ context.Context) (*protocol.AgentsResponse, error) {
	return m.agents, nil
}
func (m *mockAPI) GetWorkflows(_ context.Context) (*protocol.WorkflowsResponse, error) {
	return m.workflows, nil
}
func (m *mockAPI) ReloadWorkflows(_ context.Context) error {
	return m.reloadErr
}

func TestGetStatus(t *testing.T) {
	s := &MCPServer{
		api: &mockAPI{
			status: &protocol.StatusResponse{
				Status:        "ok",
				Uptime:        "1h30m",
				NATSRunning:   true,
				AgentCount:    2,
				WorkflowCount: 3,
			},
		},
	}

	result, err := s.handleGetStatus(context.Background(), mcplib.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success, got error result")
	}

	text := result.Content[0].(mcplib.TextContent).Text
	var status protocol.StatusResponse
	if err := json.Unmarshal([]byte(text), &status); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if status.Status != "ok" {
		t.Errorf("status = %q, want ok", status.Status)
	}
	if status.AgentCount != 2 {
		t.Errorf("agent_count = %d, want 2", status.AgentCount)
	}
	if status.WorkflowCount != 3 {
		t.Errorf("workflow_count = %d, want 3", status.WorkflowCount)
	}
}

func TestListAgents(t *testing.T) {
	s := &MCPServer{
		api: &mockAPI{
			agents: &protocol.AgentsResponse{
				Agents: []protocol.AgentInfo{
					{Name: "github-agent", Version: "0.0.12", Status: "running",
						Commands: []string{"add_label", "create_comment"}},
					{Name: "slack-agent", Version: "0.0.12", Status: "running",
						Commands: []string{"send_message"}},
				},
			},
		},
	}

	result, err := s.handleListAgents(context.Background(), mcplib.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success, got error result")
	}

	text := result.Content[0].(mcplib.TextContent).Text
	var agents []protocol.AgentInfo
	if err := json.Unmarshal([]byte(text), &agents); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
	if agents[0].Name != "github-agent" {
		t.Errorf("agents[0].name = %q, want github-agent", agents[0].Name)
	}
}

func TestListWorkflows(t *testing.T) {
	s := &MCPServer{
		api: &mockAPI{
			workflows: &protocol.WorkflowsResponse{
				Workflows: []protocol.WorkflowInfo{
					{Name: "auto-label", Handlers: 1, Patterns: []string{"sekia.events.github"}},
				},
			},
		},
	}

	result, err := s.handleListWorkflows(context.Background(), mcplib.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success, got error result")
	}

	text := result.Content[0].(mcplib.TextContent).Text
	var wfs []protocol.WorkflowInfo
	if err := json.Unmarshal([]byte(text), &wfs); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(wfs) != 1 {
		t.Fatalf("expected 1 workflow, got %d", len(wfs))
	}
	if wfs[0].Name != "auto-label" {
		t.Errorf("workflows[0].name = %q, want auto-label", wfs[0].Name)
	}
}

func TestReloadWorkflows(t *testing.T) {
	s := &MCPServer{
		api: &mockAPI{},
	}

	result, err := s.handleReloadWorkflows(context.Background(), mcplib.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("expected success, got error result")
	}

	text := result.Content[0].(mcplib.TextContent).Text
	if text != `{"status":"reloaded"}` {
		t.Errorf("result = %q, want reload success", text)
	}
}

func TestPublishEvent(t *testing.T) {
	// Start in-process NATS via a test daemon (reuse existing pattern).
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "d.sock")

	cfg := server.Config{
		Server: server.ServerConfig{Socket: socketPath},
		NATS:   server.NATSConfig{Embedded: true, DataDir: filepath.Join(tmpDir, "nats")},
	}

	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()
	d := server.NewDaemon(cfg, logger)

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run() }()

	select {
	case <-d.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not start")
	}
	t.Cleanup(func() {
		d.Stop()
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("daemon error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("daemon did not shut down")
		}
	})

	// Connect NATS for the MCP server.
	nc, err := nats.Connect(d.NATSClientURL(), d.NATSConnectOpts()...)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	defer nc.Close()

	s := &MCPServer{
		api: &mockAPI{},
		nc:  nc,
	}

	// Subscribe to the event subject to verify publish.
	received := make(chan []byte, 1)
	sub, err := nc.Subscribe("sekia.events.test", func(msg *nats.Msg) {
		received <- msg.Data
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()
	nc.Flush()

	// Call publish_event tool.
	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"source":     "test",
		"event_type": "test.ping",
		"payload":    map[string]any{"message": "hello"},
	}

	result, err := s.handlePublishEvent(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content[0].(mcplib.TextContent).Text)
	}

	select {
	case data := <-received:
		var ev protocol.Event
		json.Unmarshal(data, &ev)
		if ev.Type != "test.ping" {
			t.Errorf("event.type = %q, want test.ping", ev.Type)
		}
		if ev.Source != "mcp:test" {
			t.Errorf("event.source = %q, want mcp:test", ev.Source)
		}
		if ev.Payload["message"] != "hello" {
			t.Errorf("payload.message = %v, want hello", ev.Payload["message"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestSendCommand(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "d.sock")

	cfg := server.Config{
		Server: server.ServerConfig{Socket: socketPath},
		NATS:   server.NATSConfig{Embedded: true, DataDir: filepath.Join(tmpDir, "nats")},
	}

	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()
	d := server.NewDaemon(cfg, logger)

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run() }()

	select {
	case <-d.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not start")
	}
	t.Cleanup(func() {
		d.Stop()
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("daemon error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("daemon did not shut down")
		}
	})

	nc, err := nats.Connect(d.NATSClientURL(), d.NATSConnectOpts()...)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	defer nc.Close()

	s := &MCPServer{
		api: &mockAPI{},
		nc:  nc,
	}

	// Subscribe to the command subject.
	received := make(chan []byte, 1)
	sub, err := nc.Subscribe("sekia.commands.github-agent", func(msg *nats.Msg) {
		received <- msg.Data
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()
	nc.Flush()

	// Call send_command tool.
	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"agent":   "github-agent",
		"command": "add_label",
		"payload": map[string]any{"owner": "sekia-ai", "repo": "sekia", "number": float64(1), "label": "bug"},
	}

	result, err := s.handleSendCommand(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.Content[0].(mcplib.TextContent).Text)
	}

	select {
	case data := <-received:
		var cmd map[string]any
		json.Unmarshal(data, &cmd)
		if cmd["command"] != "add_label" {
			t.Errorf("command = %v, want add_label", cmd["command"])
		}
		if cmd["source"] != "mcp" {
			t.Errorf("source = %v, want mcp", cmd["source"])
		}
		payload := cmd["payload"].(map[string]any)
		if payload["label"] != "bug" {
			t.Errorf("payload.label = %v, want bug", payload["label"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for command")
	}
}

func TestSendCommandMissingPayload(t *testing.T) {
	s := &MCPServer{api: &mockAPI{}}

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"agent":   "github-agent",
		"command": "add_label",
	}

	result, err := s.handleSendCommand(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result for missing payload")
	}
}

func TestMCPEndToEnd(t *testing.T) {
	wfDir := t.TempDir()

	// Write a workflow that echoes events back as commands.
	workflowCode := `
sekia.on("sekia.events.test", function(event)
	sekia.command("test-agent", "echo", {
		original_type = event.type,
		message = event.payload.message,
	})
end)
`
	os.WriteFile(filepath.Join(wfDir, "echo.lua"), []byte(workflowCode), 0644)

	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "d.sock")

	cfg := server.Config{
		Server:    server.ServerConfig{Socket: socketPath},
		NATS:      server.NATSConfig{Embedded: true, DataDir: filepath.Join(tmpDir, "nats")},
		Workflows: server.WorkflowConfig{Dir: wfDir, HotReload: false},
	}

	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()
	d := server.NewDaemon(cfg, logger)

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run() }()

	select {
	case <-d.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not start")
	}
	t.Cleanup(func() {
		d.Stop()
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("daemon error: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("daemon did not shut down")
		}
	})

	// Connect NATS and create MCP server with real API client.
	nc, err := nats.Connect(d.NATSClientURL(), d.NATSConnectOpts()...)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	defer nc.Close()

	apiClient := NewAPIClient(socketPath)
	s := &MCPServer{
		api:    apiClient,
		nc:     nc,
		logger: logger,
	}

	// Connect a test agent that listens for commands.
	testAgent, err := agent.New(agent.Config{
		NATSUrl:  d.NATSClientURL(),
		NATSOpts: d.NATSConnectOpts(),
	}, "test-agent", "0.0.12", []string{"testing"}, []string{"echo"}, logger)
	if err != nil {
		t.Fatalf("create test agent: %v", err)
	}
	defer testAgent.Close()

	commandReceived := make(chan []byte, 1)
	sub, err := testAgent.Conn().Subscribe("sekia.commands.test-agent", func(msg *nats.Msg) {
		commandReceived <- msg.Data
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Unsubscribe()

	time.Sleep(500 * time.Millisecond)

	// Test 1: get_status via real daemon API.
	statusResult, err := s.handleGetStatus(context.Background(), mcplib.CallToolRequest{})
	if err != nil {
		t.Fatalf("get_status error: %v", err)
	}
	if statusResult.IsError {
		t.Fatalf("get_status failed: %s", statusResult.Content[0].(mcplib.TextContent).Text)
	}
	var status protocol.StatusResponse
	json.Unmarshal([]byte(statusResult.Content[0].(mcplib.TextContent).Text), &status)
	if status.Status != "ok" {
		t.Errorf("status = %q, want ok", status.Status)
	}
	if status.WorkflowCount != 1 {
		t.Errorf("workflow_count = %d, want 1", status.WorkflowCount)
	}

	// Test 2: list_agents — should see test-agent.
	agentsResult, err := s.handleListAgents(context.Background(), mcplib.CallToolRequest{})
	if err != nil {
		t.Fatalf("list_agents error: %v", err)
	}
	var agents []protocol.AgentInfo
	json.Unmarshal([]byte(agentsResult.Content[0].(mcplib.TextContent).Text), &agents)
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].Name != "test-agent" {
		t.Errorf("agent name = %q, want test-agent", agents[0].Name)
	}

	// Test 3: list_workflows — should see echo workflow.
	wfResult, err := s.handleListWorkflows(context.Background(), mcplib.CallToolRequest{})
	if err != nil {
		t.Fatalf("list_workflows error: %v", err)
	}
	var wfs []protocol.WorkflowInfo
	json.Unmarshal([]byte(wfResult.Content[0].(mcplib.TextContent).Text), &wfs)
	if len(wfs) != 1 {
		t.Fatalf("expected 1 workflow, got %d", len(wfs))
	}
	if wfs[0].Name != "echo" {
		t.Errorf("workflow name = %q, want echo", wfs[0].Name)
	}

	// Test 4: publish_event → workflow → command to test-agent.
	pubReq := mcplib.CallToolRequest{}
	pubReq.Params.Arguments = map[string]any{
		"source":     "test",
		"event_type": "integration.test",
		"payload":    map[string]any{"message": "hello from MCP"},
	}

	pubResult, err := s.handlePublishEvent(context.Background(), pubReq)
	if err != nil {
		t.Fatalf("publish_event error: %v", err)
	}
	if pubResult.IsError {
		t.Fatalf("publish_event failed: %s", pubResult.Content[0].(mcplib.TextContent).Text)
	}

	select {
	case cmdData := <-commandReceived:
		var cmd map[string]any
		json.Unmarshal(cmdData, &cmd)
		if cmd["command"] != "echo" {
			t.Errorf("command = %v, want echo", cmd["command"])
		}
		payload := cmd["payload"].(map[string]any)
		if payload["message"] != "hello from MCP" {
			t.Errorf("message = %v, want 'hello from MCP'", payload["message"])
		}
		if payload["original_type"] != "integration.test" {
			t.Errorf("original_type = %v, want integration.test", payload["original_type"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for workflow command")
	}
}
