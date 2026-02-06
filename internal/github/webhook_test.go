package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

func TestVerifySignature(t *testing.T) {
	secret := []byte("mysecret")
	payload := []byte(`{"action":"opened"}`)

	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	validSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !verifySignature(payload, secret, validSig) {
		t.Error("expected valid signature to pass")
	}
	if verifySignature(payload, secret, "sha256=invalid") {
		t.Error("expected invalid signature to fail")
	}
	if verifySignature(payload, secret, "invalid") {
		t.Error("expected missing sha256= prefix to fail")
	}
	if verifySignature(payload, secret, "") {
		t.Error("expected empty signature to fail")
	}
}

func TestWebhookHandlerIssueOpened(t *testing.T) {
	received := make(chan protocol.Event, 1)
	ws := NewWebhookServer(WebhookConfig{Path: "/webhook"}, func(ev protocol.Event) {
		received <- ev
	}, zerolog.Nop())

	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhook", ws.handleWebhook)

	payload := issueWebhookJSON("opened", "myorg", "myrepo", 42, "Test issue", "alice")

	req := httptest.NewRequest("POST", "/webhook", strings.NewReader(string(payload)))
	req.Header.Set("X-GitHub-Event", "issues")
	req.Header.Set("X-GitHub-Delivery", "test-delivery-1")
	w := httptest.NewRecorder()

	ws.handleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "accepted" {
		t.Errorf("response status = %s, want accepted", resp["status"])
	}

	select {
	case ev := <-received:
		if ev.Type != "github.issue.opened" {
			t.Errorf("event type = %s, want github.issue.opened", ev.Type)
		}
	default:
		t.Fatal("no event received")
	}
}

func TestWebhookHandlerUnsupportedEvent(t *testing.T) {
	ws := NewWebhookServer(WebhookConfig{Path: "/webhook"}, func(ev protocol.Event) {
		t.Error("onEvent should not be called for unsupported events")
	}, zerolog.Nop())

	req := httptest.NewRequest("POST", "/webhook", strings.NewReader(`{}`))
	req.Header.Set("X-GitHub-Event", "deployment")
	w := httptest.NewRecorder()

	ws.handleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "ignored" {
		t.Errorf("response status = %s, want ignored", resp["status"])
	}
}

func TestWebhookHandlerInvalidSignature(t *testing.T) {
	ws := NewWebhookServer(WebhookConfig{
		Path:   "/webhook",
		Secret: "mysecret",
	}, func(ev protocol.Event) {
		t.Error("onEvent should not be called with invalid signature")
	}, zerolog.Nop())

	payload := issueWebhookJSON("opened", "myorg", "myrepo", 1, "Test", "alice")

	req := httptest.NewRequest("POST", "/webhook", strings.NewReader(string(payload)))
	req.Header.Set("X-GitHub-Event", "issues")
	req.Header.Set("X-Hub-Signature-256", "sha256=invalid")
	w := httptest.NewRecorder()

	ws.handleWebhook(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestWebhookHandlerValidSignature(t *testing.T) {
	secret := "mysecret"
	received := make(chan protocol.Event, 1)

	ws := NewWebhookServer(WebhookConfig{
		Path:   "/webhook",
		Secret: secret,
	}, func(ev protocol.Event) {
		received <- ev
	}, zerolog.Nop())

	payload := issueWebhookJSON("opened", "myorg", "myrepo", 1, "Test", "alice")

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest("POST", "/webhook", strings.NewReader(string(payload)))
	req.Header.Set("X-GitHub-Event", "issues")
	req.Header.Set("X-Hub-Signature-256", sig)
	w := httptest.NewRecorder()

	ws.handleWebhook(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	select {
	case ev := <-received:
		if ev.Type != "github.issue.opened" {
			t.Errorf("event type = %s, want github.issue.opened", ev.Type)
		}
	default:
		t.Fatal("no event received")
	}
}

func TestWebhookHandlerMissingEventHeader(t *testing.T) {
	ws := NewWebhookServer(WebhookConfig{Path: "/webhook"}, func(ev protocol.Event) {
		t.Error("onEvent should not be called without event header")
	}, zerolog.Nop())

	req := httptest.NewRequest("POST", "/webhook", strings.NewReader(`{}`))
	w := httptest.NewRecorder()

	ws.handleWebhook(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
