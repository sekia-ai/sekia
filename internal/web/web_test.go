package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sekia-ai/sekia/internal/registry"
	"github.com/sekia-ai/sekia/internal/workflow"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
)

func setupTest(t *testing.T) (*Server, *nats.Conn) {
	return setupTestWithAuth(t, "", "")
}

func setupTestWithAuth(t *testing.T, username, password string) (*Server, *nats.Conn) {
	t.Helper()

	ns, err := natsserver.NewServer(&natsserver.Options{
		DontListen: true,
		JetStream:  true,
		StoreDir:   t.TempDir(),
		NoLog:      true,
		NoSigs:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}
	t.Cleanup(ns.Shutdown)

	nc, err := nats.Connect(ns.ClientURL(), nats.InProcessServer(ns))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)

	logger := zerolog.Nop()

	reg, err := registry.New(nc, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reg.Close)

	eng := workflow.New(nc, t.TempDir(), nil, 0, "", logger)
	if err := eng.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Stop)

	srv := New(Config{Listen: ":0", Username: username, Password: password}, reg, eng, nc, time.Now(), logger)
	return srv, nc
}

func TestDashboardRenders(t *testing.T) {
	srv, _ := setupTest(t)

	ts := httptest.NewServer(srv.httpServer.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/web")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	if !strings.Contains(html, "sekia Dashboard") {
		t.Error("expected page title 'sekia Dashboard'")
	}
	if !strings.Contains(html, "System Status") {
		t.Error("expected 'System Status' section")
	}
	if !strings.Contains(html, "Connected Agents") {
		t.Error("expected 'Connected Agents' section")
	}
	if !strings.Contains(html, "Loaded Workflows") {
		t.Error("expected 'Loaded Workflows' section")
	}
	if !strings.Contains(html, "Live Events") {
		t.Error("expected 'Live Events' section")
	}
}

func TestPartialStatus(t *testing.T) {
	srv, _ := setupTest(t)

	ts := httptest.NewServer(srv.httpServer.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/web/partials/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "System Status") {
		t.Error("expected 'System Status' in partial response")
	}
}

func TestPartialAgents(t *testing.T) {
	srv, _ := setupTest(t)

	ts := httptest.NewServer(srv.httpServer.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/web/partials/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "No agents connected") {
		t.Error("expected 'No agents connected' empty state")
	}
}

func TestPartialWorkflows(t *testing.T) {
	srv, _ := setupTest(t)

	ts := httptest.NewServer(srv.httpServer.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/web/partials/workflows")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "No workflows loaded") {
		t.Error("expected 'No workflows loaded' empty state")
	}
}

func TestStaticAssets(t *testing.T) {
	srv, _ := setupTest(t)

	ts := httptest.NewServer(srv.httpServer.Handler)
	defer ts.Close()

	for _, path := range []string{"/web/static/htmx.min.js", "/web/static/alpine.min.js", "/web/static/style.css"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("GET %s: expected 200, got %d", path, resp.StatusCode)
		}
	}
}

func TestEventBus(t *testing.T) {
	eb := NewEventBus(5)

	ch, unsub, err := eb.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer unsub()

	eb.Publish([]byte(`{"type":"test"}`))

	select {
	case data := <-ch:
		if string(data) != `{"type":"test"}` {
			t.Errorf("unexpected data: %s", data)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}

	// Test ring buffer.
	for i := 0; i < 10; i++ {
		eb.Publish([]byte(`{"type":"flood"}`))
	}
	recent := eb.Recent()
	if len(recent) != 5 {
		t.Errorf("expected 5 recent events, got %d", len(recent))
	}
}

func TestAuthRequired(t *testing.T) {
	srv, _ := setupTestWithAuth(t, "admin", "secret")

	ts := httptest.NewServer(srv.httpServer.Handler)
	defer ts.Close()

	// Without credentials → 401.
	resp, err := http.Get(ts.URL + "/web")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 without auth, got %d", resp.StatusCode)
	}
	if resp.Header.Get("WWW-Authenticate") == "" {
		t.Error("expected WWW-Authenticate header")
	}

	// With wrong credentials → 401.
	req, _ := http.NewRequest("GET", ts.URL+"/web", nil)
	req.SetBasicAuth("admin", "wrong")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 with wrong password, got %d", resp.StatusCode)
	}

	// With correct credentials → 200.
	req, _ = http.NewRequest("GET", ts.URL+"/web", nil)
	req.SetBasicAuth("admin", "secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 with correct auth, got %d", resp.StatusCode)
	}
}

func TestSecurityHeaders(t *testing.T) {
	srv, _ := setupTest(t)

	ts := httptest.NewServer(srv.httpServer.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/web")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	checks := map[string]string{
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":          "DENY",
		"Content-Security-Policy":  "default-src 'none'; script-src 'self' 'unsafe-eval'; style-src 'self'; img-src 'self'; connect-src 'self'",
		"Strict-Transport-Security": "max-age=63072000; includeSubDomains",
	}
	for header, want := range checks {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestCSRFCookieSet(t *testing.T) {
	srv, _ := setupTest(t)

	ts := httptest.NewServer(srv.httpServer.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/web")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	var csrfCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "sekia_csrf" {
			csrfCookie = c
			break
		}
	}
	if csrfCookie == nil {
		t.Fatal("expected sekia_csrf cookie to be set")
	}
	if len(csrfCookie.Value) != 64 { // 32 bytes hex-encoded
		t.Errorf("expected 64-char token, got %d chars", len(csrfCookie.Value))
	}
}

func TestCSRFRejectsPostWithoutToken(t *testing.T) {
	srv, _ := setupTest(t)

	ts := httptest.NewServer(srv.httpServer.Handler)
	defer ts.Close()

	// POST without CSRF token → 403.
	req, _ := http.NewRequest("POST", ts.URL+"/web", nil)
	req.AddCookie(&http.Cookie{Name: "sekia_csrf", Value: "sometoken"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("expected 403 without CSRF header, got %d", resp.StatusCode)
	}

	// POST with matching CSRF header → passes CSRF check (may 404 since no POST route).
	req, _ = http.NewRequest("POST", ts.URL+"/web", nil)
	req.AddCookie(&http.Cookie{Name: "sekia_csrf", Value: "sometoken"})
	req.Header.Set("X-CSRF-Token", "sometoken")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == 403 {
		t.Fatal("expected CSRF check to pass with matching token")
	}
}

func TestSSEConnectionLimit(t *testing.T) {
	eb := NewEventBus(5)

	// Fill up to the limit.
	unsubs := make([]func(), 0, maxSSEClients)
	for i := 0; i < maxSSEClients; i++ {
		_, unsub, err := eb.Subscribe()
		if err != nil {
			t.Fatalf("subscribe %d: unexpected error: %v", i, err)
		}
		unsubs = append(unsubs, unsub)
	}

	// Next subscribe should fail.
	_, _, err := eb.Subscribe()
	if err != ErrTooManyClients {
		t.Fatalf("expected ErrTooManyClients, got %v", err)
	}

	// Unsubscribe one → should be able to subscribe again.
	unsubs[0]()
	_, unsub, err := eb.Subscribe()
	if err != nil {
		t.Fatalf("expected subscribe to succeed after unsub: %v", err)
	}
	unsub()

	for _, fn := range unsubs[1:] {
		fn()
	}
}
