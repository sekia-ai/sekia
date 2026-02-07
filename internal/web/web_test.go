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

	eng := workflow.New(nc, t.TempDir(), logger)
	if err := eng.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Stop)

	srv := New(":0", reg, eng, nc, time.Now(), logger)
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

	if !strings.Contains(html, "Sekia Dashboard") {
		t.Error("expected page title 'Sekia Dashboard'")
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

	ch, unsub := eb.Subscribe()
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
