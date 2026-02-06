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

	// Wait for socket to appear.
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
