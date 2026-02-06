package protocol

import "time"

// StatusResponse is returned by GET /api/v1/status.
type StatusResponse struct {
	Status        string    `json:"status"`
	Uptime        string    `json:"uptime"`
	NATSRunning   bool      `json:"nats_running"`
	StartedAt     time.Time `json:"started_at"`
	AgentCount    int       `json:"agent_count"`
	WorkflowCount int       `json:"workflow_count"`
}

// AgentInfo is one entry in the GET /api/v1/agents response.
type AgentInfo struct {
	Name            string    `json:"name"`
	Version         string    `json:"version"`
	Status          string    `json:"status"`
	Capabilities    []string  `json:"capabilities"`
	Commands        []string  `json:"commands"`
	RegisteredAt    time.Time `json:"registered_at"`
	LastHeartbeat   time.Time `json:"last_heartbeat"`
	EventsProcessed int64    `json:"events_processed"`
	Errors          int64    `json:"errors"`
}

// AgentsResponse is returned by GET /api/v1/agents.
type AgentsResponse struct {
	Agents []AgentInfo `json:"agents"`
}

// WorkflowInfo is one entry in the GET /api/v1/workflows response.
type WorkflowInfo struct {
	Name     string   `json:"name"`
	FilePath string   `json:"file_path"`
	Handlers int      `json:"handlers"`
	Patterns []string `json:"patterns"`
	LoadedAt time.Time `json:"loaded_at"`
	Events   int64    `json:"events"`
	Errors   int64    `json:"errors"`
}

// WorkflowsResponse is returned by GET /api/v1/workflows.
type WorkflowsResponse struct {
	Workflows []WorkflowInfo `json:"workflows"`
}
