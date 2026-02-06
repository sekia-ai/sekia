package api

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/sekia-ai/sekia/internal/registry"
	"github.com/sekia-ai/sekia/internal/workflow"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

// Server serves the sekiad control API over a Unix socket.
type Server struct {
	socketPath string
	registry   *registry.Registry
	engine     *workflow.Engine
	startedAt  time.Time
	httpServer *http.Server
	logger     zerolog.Logger
}

// New creates an API server. The engine parameter may be nil if workflows are disabled.
func New(socketPath string, reg *registry.Registry, engine *workflow.Engine, startedAt time.Time, logger zerolog.Logger) *Server {
	s := &Server{
		socketPath: socketPath,
		registry:   reg,
		engine:     engine,
		startedAt:  startedAt,
		logger:     logger.With().Str("component", "api").Logger(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	mux.HandleFunc("GET /api/v1/agents", s.handleAgents)
	mux.HandleFunc("GET /api/v1/workflows", s.handleWorkflows)
	mux.HandleFunc("POST /api/v1/workflows/reload", s.handleWorkflowReload)

	s.httpServer = &http.Server{Handler: mux}
	return s
}

// Start begins listening on the Unix socket. Blocks until Shutdown.
func (s *Server) Start() error {
	os.Remove(s.socketPath)

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return err
	}
	os.Chmod(s.socketPath, 0600)

	s.logger.Info().Str("socket", s.socketPath).Msg("API server listening")
	return s.httpServer.Serve(ln)
}

// Shutdown gracefully stops the API server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	resp := protocol.StatusResponse{
		Status:      "ok",
		Uptime:      time.Since(s.startedAt).Truncate(time.Second).String(),
		NATSRunning: true,
		StartedAt:   s.startedAt,
		AgentCount:  s.registry.Count(),
	}
	if s.engine != nil {
		resp.WorkflowCount = s.engine.Count()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	resp := protocol.AgentsResponse{
		Agents: s.registry.Agents(),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleWorkflows(w http.ResponseWriter, r *http.Request) {
	var workflows []protocol.WorkflowInfo
	if s.engine != nil {
		for _, wf := range s.engine.Workflows() {
			workflows = append(workflows, protocol.WorkflowInfo{
				Name:     wf.Name,
				FilePath: wf.FilePath,
				Handlers: wf.Handlers,
				Patterns: wf.Patterns,
				LoadedAt: wf.LoadedAt,
				Events:   wf.Events,
				Errors:   wf.Errors,
			})
		}
	}
	if workflows == nil {
		workflows = []protocol.WorkflowInfo{}
	}
	resp := protocol.WorkflowsResponse{Workflows: workflows}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleWorkflowReload(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		http.Error(w, "workflow engine not enabled", http.StatusServiceUnavailable)
		return
	}
	if err := s.engine.ReloadAll(); err != nil {
		s.logger.Error().Err(err).Msg("workflow reload failed")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "reloaded"})
}
