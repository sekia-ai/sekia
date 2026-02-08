package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

// DaemonAPI is the interface for reading from the sekiad daemon API.
// Implemented by APIClient; tests can provide a mock.
type DaemonAPI interface {
	GetStatus(ctx context.Context) (*protocol.StatusResponse, error)
	GetAgents(ctx context.Context) (*protocol.AgentsResponse, error)
	GetWorkflows(ctx context.Context) (*protocol.WorkflowsResponse, error)
	ReloadWorkflows(ctx context.Context) error
}

// APIClient talks to the sekiad daemon over its Unix socket HTTP API.
type APIClient struct {
	client *http.Client
}

// NewAPIClient creates an APIClient connected to the daemon's Unix socket.
func NewAPIClient(socketPath string) *APIClient {
	return &APIClient{
		client: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

func (c *APIClient) GetStatus(ctx context.Context) (*protocol.StatusResponse, error) {
	var resp protocol.StatusResponse
	if err := c.getJSON(ctx, "/api/v1/status", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *APIClient) GetAgents(ctx context.Context) (*protocol.AgentsResponse, error) {
	var resp protocol.AgentsResponse
	if err := c.getJSON(ctx, "/api/v1/agents", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *APIClient) GetWorkflows(ctx context.Context) (*protocol.WorkflowsResponse, error) {
	var resp protocol.WorkflowsResponse
	if err := c.getJSON(ctx, "/api/v1/workflows", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *APIClient) ReloadWorkflows(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://sekiad/api/v1/workflows/reload", nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("reload request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("reload returned status %d", resp.StatusCode)
	}
	return nil
}

func (c *APIClient) getJSON(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://sekiad"+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}
