package workflow

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// startTestNATS starts an in-process NATS server for testing.
func startTestNATS(t *testing.T) (*server.Server, *nats.Conn) {
	t.Helper()

	opts := &server.Options{
		Host:           "127.0.0.1",
		Port:           -1, // random port
		NoLog:          true,
		NoSigs:         true,
		DontListen:     true,
		JetStream:      false,
		MaxControlLine: 2048,
	}
	ns, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server not ready")
	}

	nc, err := nats.Connect("", nats.InProcessServer(ns))
	if err != nil {
		ns.Shutdown()
		t.Fatal(err)
	}

	t.Cleanup(func() {
		nc.Close()
		ns.Shutdown()
	})

	return ns, nc
}

func TestLuaOn(t *testing.T) {
	_, nc := startTestNATS(t)

	L := NewSandboxedState("test-wf", testLogger())
	defer L.Close()

	ctx := &moduleContext{
		name:   "test-wf",
		nc:     nc,
		logger: testLogger(),
	}
	registerSekiaModule(L, ctx)

	err := L.DoString(`
		sekia.on("sekia.events.test", function(event)
			-- handler body
		end)
		sekia.on("sekia.events.*", function(event)
			-- wildcard handler
		end)
	`)
	if err != nil {
		t.Fatalf("DoString: %v", err)
	}

	if len(ctx.handlers) != 2 {
		t.Fatalf("expected 2 handlers, got %d", len(ctx.handlers))
	}
	if ctx.handlers[0].Pattern != "sekia.events.test" {
		t.Errorf("handler[0].Pattern = %s, want sekia.events.test", ctx.handlers[0].Pattern)
	}
	if ctx.handlers[1].Pattern != "sekia.events.*" {
		t.Errorf("handler[1].Pattern = %s, want sekia.events.*", ctx.handlers[1].Pattern)
	}
}

func TestLuaPublish(t *testing.T) {
	_, nc := startTestNATS(t)

	L := NewSandboxedState("test-wf", testLogger())
	defer L.Close()

	ctx := &moduleContext{
		name:   "test-wf",
		nc:     nc,
		logger: testLogger(),
	}
	registerSekiaModule(L, ctx)

	// Subscribe to capture the published event.
	received := make(chan []byte, 1)
	sub, err := nc.Subscribe("sekia.events.workflow", func(msg *nats.Msg) {
		received <- msg.Data
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	err = L.DoString(`
		sekia.publish("sekia.events.workflow", "test.event", {
			key = "value",
			num = 42,
		})
	`)
	if err != nil {
		t.Fatalf("DoString: %v", err)
	}
	nc.Flush()

	select {
	case data := <-received:
		var ev map[string]any
		if err := json.Unmarshal(data, &ev); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if ev["type"] != "test.event" {
			t.Errorf("type = %v, want test.event", ev["type"])
		}
		if ev["source"] != "workflow:test-wf" {
			t.Errorf("source = %v, want workflow:test-wf", ev["source"])
		}
		payload := ev["payload"].(map[string]any)
		if payload["key"] != "value" {
			t.Errorf("payload.key = %v, want value", payload["key"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for published event")
	}
}

func TestLuaCommand(t *testing.T) {
	_, nc := startTestNATS(t)

	L := NewSandboxedState("test-wf", testLogger())
	defer L.Close()

	ctx := &moduleContext{
		name:   "test-wf",
		nc:     nc,
		logger: testLogger(),
	}
	registerSekiaModule(L, ctx)

	// Subscribe to the target agent's command subject.
	received := make(chan []byte, 1)
	sub, err := nc.Subscribe("sekia.commands.github-agent", func(msg *nats.Msg) {
		received <- msg.Data
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	err = L.DoString(`
		sekia.command("github-agent", "add_label", {
			issue = 42,
			label = "bug",
		})
	`)
	if err != nil {
		t.Fatalf("DoString: %v", err)
	}
	nc.Flush()

	select {
	case data := <-received:
		var msg map[string]any
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if msg["command"] != "add_label" {
			t.Errorf("command = %v, want add_label", msg["command"])
		}
		if msg["source"] != "workflow:test-wf" {
			t.Errorf("source = %v, want workflow:test-wf", msg["source"])
		}
		payload := msg["payload"].(map[string]any)
		if payload["label"] != "bug" {
			t.Errorf("payload.label = %v, want bug", payload["label"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for command")
	}
}

func TestLuaLog(t *testing.T) {
	_, nc := startTestNATS(t)

	L := NewSandboxedState("test-wf", testLogger())
	defer L.Close()

	ctx := &moduleContext{
		name:   "test-wf",
		nc:     nc,
		logger: testLogger(),
	}
	registerSekiaModule(L, ctx)

	// Verify that log calls don't error.
	for _, level := range []string{"debug", "info", "warn", "error"} {
		err := L.DoString(`sekia.log("` + level + `", "test message at ` + level + `")`)
		if err != nil {
			t.Errorf("sekia.log(%s): %v", level, err)
		}
	}
}

func TestLuaName(t *testing.T) {
	_, nc := startTestNATS(t)

	L := NewSandboxedState("my-workflow", testLogger())
	defer L.Close()

	ctx := &moduleContext{
		name:   "my-workflow",
		nc:     nc,
		logger: testLogger(),
	}
	registerSekiaModule(L, ctx)

	err := L.DoString(`assert(sekia.name == "my-workflow", "expected my-workflow, got " .. tostring(sekia.name))`)
	if err != nil {
		t.Fatalf("DoString: %v", err)
	}
}
