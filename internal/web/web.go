package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
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
	Password string // #nosec G117 -- HTTP Basic Auth password (empty = no auth).
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

	s.httpServer = &http.Server{
		Handler:           s.securityMiddleware(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

// securityMiddleware adds security headers, optional HTTP Basic Auth, and CSRF
// protection (double-submit cookie) to all responses.
func (s *Server) securityMiddleware(next http.Handler) http.Handler {
	authEnabled := s.username != "" && s.password != ""
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)

		if authEnabled && !s.checkBasicAuth(w, r) {
			return
		}

		csrfToken, ok := ensureCSRFCookie(w, r)
		if !ok {
			return
		}
		if !validateCSRF(w, r, csrfToken) {
			return
		}

		next.ServeHTTP(w, r)
	})
}

// setSecurityHeaders writes standard security response headers.
func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; script-src 'self' 'unsafe-eval'; style-src 'self'; img-src 'self'; connect-src 'self'")
	w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
}

// checkBasicAuth validates HTTP Basic Auth credentials. Returns false (and
// writes a 401 response) when credentials are missing or wrong.
func (s *Server) checkBasicAuth(w http.ResponseWriter, r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	if !ok ||
		subtle.ConstantTimeCompare([]byte(user), []byte(s.username)) != 1 ||
		subtle.ConstantTimeCompare([]byte(pass), []byte(s.password)) != 1 {
		w.Header().Set("WWW-Authenticate", `Basic realm="sekia"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

// ensureCSRFCookie returns the current CSRF token (from cookie or freshly
// generated) and sets the cookie when needed. Returns ("", false) on error.
func ensureCSRFCookie(w http.ResponseWriter, r *http.Request) (string, bool) {
	if c, err := r.Cookie("sekia_csrf"); err == nil && c.Value != "" {
		return c.Value, true
	}
	token, err := generateCSRFToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return "", false
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "sekia_csrf",
		Value:    token,
		Path:     "/web",
		HttpOnly: false, // Must be readable by JS to submit as header.
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
	})
	return token, true
}

// validateCSRF checks the X-CSRF-Token header against the cookie value for
// state-changing HTTP methods. Returns false (and writes 403) on mismatch.
func validateCSRF(w http.ResponseWriter, r *http.Request, cookieToken string) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}
	headerToken := r.Header.Get("X-CSRF-Token")
	if subtle.ConstantTimeCompare([]byte(headerToken), []byte(cookieToken)) != 1 {
		http.Error(w, "CSRF token mismatch", http.StatusForbidden)
		return false
	}
	return true
}

// generateCSRFToken returns a 32-byte hex-encoded random token.
func generateCSRFToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
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
