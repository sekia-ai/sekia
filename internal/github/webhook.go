package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/rs/zerolog"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

const maxWebhookBodySize = 10 << 20 // 10 MB

// WebhookServer receives GitHub webhook deliveries over HTTP.
type WebhookServer struct {
	listenAddr string
	secret     []byte
	path       string
	onEvent    func(protocol.Event)
	httpServer *http.Server
	listener   net.Listener
	logger     zerolog.Logger
}

// NewWebhookServer creates a webhook server. onEvent is called for each mapped event.
func NewWebhookServer(cfg WebhookConfig, onEvent func(protocol.Event), logger zerolog.Logger) *WebhookServer {
	return &WebhookServer{
		listenAddr: cfg.Listen,
		secret:     []byte(cfg.Secret),
		path:       cfg.Path,
		onEvent:    onEvent,
		logger:     logger.With().Str("component", "webhook").Logger(),
	}
}

// Listen binds the TCP socket. Call Serve() afterwards to accept connections.
func (ws *WebhookServer) Listen() error {
	ln, err := net.Listen("tcp", ws.listenAddr)
	if err != nil {
		return err
	}
	ws.listener = ln
	return nil
}

// Serve accepts connections on the listener created by Listen. Blocks until shut down.
func (ws *WebhookServer) Serve() error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+ws.path, ws.handleWebhook)
	ws.httpServer = &http.Server{Handler: mux}
	ws.logger.Info().Str("addr", ws.listener.Addr().String()).Msg("webhook server listening")
	return ws.httpServer.Serve(ws.listener)
}

// Start binds and serves. It blocks until the server is shut down.
func (ws *WebhookServer) Start() error {
	if err := ws.Listen(); err != nil {
		return err
	}
	return ws.Serve()
}

// Addr returns the listener address. Only valid after Start is called.
func (ws *WebhookServer) Addr() string {
	if ws.listener == nil {
		return ""
	}
	return ws.listener.Addr().String()
}

// Shutdown gracefully stops the webhook server.
func (ws *WebhookServer) Shutdown(ctx context.Context) {
	if ws.httpServer != nil {
		ws.httpServer.Shutdown(ctx)
	}
}

func (ws *WebhookServer) handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodySize))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	// Verify HMAC-SHA256 signature. Reject all requests if no secret is configured.
	if len(ws.secret) == 0 {
		ws.logger.Error().Msg("webhook secret not configured; rejecting request")
		http.Error(w, "webhook secret not configured", http.StatusForbidden)
		return
	}
	sig := r.Header.Get("X-Hub-Signature-256")
	if !verifySignature(body, ws.secret, sig) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	if eventType == "" {
		http.Error(w, "missing X-GitHub-Event header", http.StatusBadRequest)
		return
	}

	delivery := r.Header.Get("X-GitHub-Delivery")

	ev, ok := MapWebhookEvent(eventType, body)
	if !ok {
		ws.logger.Debug().
			Str("event_type", eventType).
			Str("delivery", delivery).
			Msg("ignoring unsupported event")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ignored"})
		return
	}

	ws.onEvent(ev)

	ws.logger.Info().
		Str("event_type", eventType).
		Str("sekia_type", ev.Type).
		Str("delivery", delivery).
		Msg("webhook processed")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
}

// verifySignature checks the X-Hub-Signature-256 HMAC-SHA256 signature.
func verifySignature(payload, secret []byte, signatureHeader string) bool {
	if !strings.HasPrefix(signatureHeader, "sha256=") {
		return false
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal(
		[]byte(expectedMAC),
		[]byte(strings.TrimPrefix(signatureHeader, "sha256=")),
	)
}
