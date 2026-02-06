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
	"github.com/sekia-ai/sekia/pkg/protocol"
)

// Server serves the sekiad control API over a Unix socket.
type Server struct {
	socketPath string
	registry   *registry.Registry
	startedAt  time.Time
	httpServer *http.Server
	logger     zerolog.Logger
}

// New creates an API server.
func New(socketPath string, reg *registry.Registry, startedAt time.Time, logger zerolog.Logger) *Server {
	s := &Server{
		socketPath: socketPath,
		registry:   reg,
		startedAt:  startedAt,
		logger:     logger.With().Str("component", "api").Logger(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	mux.HandleFunc("GET /api/v1/agents", s.handleAgents)

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
