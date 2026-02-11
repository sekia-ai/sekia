package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"github.com/sekia-ai/sekia/internal/registry"
	"github.com/sekia-ai/sekia/internal/workflow"
)

//go:embed static templates
var content embed.FS

// Config holds web dashboard settings passed to New.
type Config struct {
	Listen   string
	Username string // HTTP Basic Auth username (empty = no auth).
	Password string // HTTP Basic Auth password (empty = no auth).
}

// Server serves the web dashboard on a TCP port.
type Server struct {
	listen     string
	registry   *registry.Registry
	engine     *workflow.Engine
	nc         *nats.Conn
	startedAt  time.Time
	httpServer *http.Server
	logger     zerolog.Logger
	templates  *template.Template
	eventBus   *EventBus
	username   string
	password   string
}

// New creates a web UI server. The engine parameter may be nil if workflows are disabled.
// If cfg.Username and cfg.Password are non-empty, HTTP Basic Auth is required for all routes.
func New(cfg Config, reg *registry.Registry, engine *workflow.Engine,
	nc *nats.Conn, startedAt time.Time, logger zerolog.Logger) *Server {

	s := &Server{
		listen:    cfg.Listen,
		registry:  reg,
		engine:    engine,
		nc:        nc,
		startedAt: startedAt,
		logger:    logger.With().Str("component", "web").Logger(),
		eventBus:  NewEventBus(50),
		username:  cfg.Username,
		password:  cfg.Password,
	}

	funcMap := template.FuncMap{
		"join": strings.Join,
	}

	tmplFS, _ := fs.Sub(content, "templates")
	s.templates = template.Must(
		template.New("").Funcs(funcMap).ParseFS(tmplFS, "*.html", "partials/*.html"),
	)

	mux := http.NewServeMux()

	staticFS, _ := fs.Sub(content, "static")
	mux.Handle("GET /web/static/", http.StripPrefix("/web/static/",
		http.FileServer(http.FS(staticFS))))

	mux.HandleFunc("GET /web", s.handleDashboard)
	mux.HandleFunc("GET /web/partials/status", s.handlePartialStatus)
	mux.HandleFunc("GET /web/partials/agents", s.handlePartialAgents)
	mux.HandleFunc("GET /web/partials/workflows", s.handlePartialWorkflows)
	mux.HandleFunc("GET /web/events/stream", s.handleEventStream)

	s.httpServer = &http.Server{Handler: s.securityMiddleware(mux)}
	return s
}

// securityMiddleware adds security headers and optional HTTP Basic Auth to all responses.
func (s *Server) securityMiddleware(next http.Handler) http.Handler {
	authEnabled := s.username != "" && s.password != ""
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")

		if authEnabled {
			user, pass, ok := r.BasicAuth()
			if !ok ||
				subtle.ConstantTimeCompare([]byte(user), []byte(s.username)) != 1 ||
				subtle.ConstantTimeCompare([]byte(pass), []byte(s.password)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="sekia"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// Start begins listening on TCP. Blocks until Shutdown or error.
func (s *Server) Start() error {
	if s.nc != nil {
		sub, err := s.nc.Subscribe("sekia.events.>", func(msg *nats.Msg) {
			s.eventBus.Publish(msg.Data)
		})
		if err != nil {
			return err
		}
		_ = sub // kept alive by NATS conn
	}

	ln, err := net.Listen("tcp", s.listen)
	if err != nil {
		return err
	}
	s.logger.Info().Str("listen", s.listen).Msg("web UI listening")
	return s.httpServer.Serve(ln)
}

// Shutdown gracefully stops the web server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
