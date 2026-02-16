package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
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
	nc         *nats.Conn
	startedAt  time.Time
	httpServer *http.Server
	logger     zerolog.Logger
}

// New creates an API server. The engine parameter may be nil if workflows are disabled.
// The nc parameter is used to publish config reload signals via NATS.
func New(socketPath string, reg *registry.Registry, engine *workflow.Engine, nc *nats.Conn, startedAt time.Time, logger zerolog.Logger) *Server {
	s := &Server{
		socketPath: socketPath,
		registry:   reg,
		engine:     engine,
		nc:         nc,
		startedAt:  startedAt,
		logger:     logger.With().Str("component", "api").Logger(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	mux.HandleFunc("GET /api/v1/agents", s.handleAgents)
	mux.HandleFunc("GET /api/v1/workflows", s.handleWorkflows)
	mux.HandleFunc("POST /api/v1/workflows/reload", s.handleWorkflowReload)
	mux.HandleFunc("POST /api/v1/config/reload", s.handleConfigReload)

	s.httpServer = &http.Server{Handler: mux}
	return s
}

// Listen creates the Unix socket listener without serving.
// Call Serve after to begin accepting connections.
//
// Security: creates the parent directory with 0700, verifies it is owned by the
// current user, and rejects symlinks at the socket path to prevent symlink attacks.
func (s *Server) Listen() (net.Listener, error) {
	dir := filepath.Dir(s.socketPath)

	// Create parent directory if needed (owner-only permissions).
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("creating socket directory: %w", err)
	}

	// Verify the parent directory is owned by the current user.
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("stat socket directory: %w", err)
	}
	stat, ok := dirInfo.Sys().(*syscall.Stat_t)
	if ok && stat.Uid != uint32(os.Getuid()) {
		return nil, fmt.Errorf("socket directory %s not owned by current user (owner uid=%d, current uid=%d)", dir, stat.Uid, os.Getuid())
	}

	// If something already exists at the socket path, reject symlinks then remove.
	if fi, err := os.Lstat(s.socketPath); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("socket path %s is a symlink, refusing to bind", s.socketPath)
		}
		os.Remove(s.socketPath)
	}

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return nil, err
	}
	os.Chmod(s.socketPath, 0600)

	s.logger.Info().Str("socket", s.socketPath).Msg("API server listening")
	return ln, nil
}

// Serve accepts connections on the given listener. Blocks until Shutdown.
func (s *Server) Serve(ln net.Listener) error {
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

func (s *Server) handleConfigReload(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("target")
	if target == "" {
		target = "*"
	}

	// Publish to the appropriate NATS subject.
	var subject string
	if target == "*" {
		subject = protocol.SubjectConfigReload
	} else {
		subject = protocol.SubjectConfigReloadAgent(target)
	}

	if err := s.nc.Publish(subject, nil); err != nil {
		s.logger.Error().Err(err).Str("target", target).Msg("config reload publish failed")
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.logger.Info().Str("target", target).Msg("config reload signal sent")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(protocol.ConfigReloadResponse{
		Status: "reload_requested",
		Target: target,
	})
}
