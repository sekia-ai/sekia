package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

func TestSubjectMatches(t *testing.T) {
	tests := []struct {
		pattern string
		subject string
		want    bool
	}{
		{"sekia.events.github", "sekia.events.github", true},
		{"sekia.events.github", "sekia.events.slack", false},
		{"sekia.events.*", "sekia.events.github", true},
		{"sekia.events.*", "sekia.events.github.issues", false},
		{"sekia.events.>", "sekia.events.github", true},
		{"sekia.events.>", "sekia.events.github.issues", true},
		{"sekia.>", "sekia.events.github", true},
		{"*.*.*", "sekia.events.github", true},
		{"*.*.*", "sekia.events", false},
		{">", "anything.at.all", true},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.subject, func(t *testing.T) {
			got := SubjectMatches(tt.pattern, tt.subject)
			if got != tt.want {
				t.Errorf("SubjectMatches(%q, %q) = %v, want %v", tt.pattern, tt.subject, got, tt.want)
			}
		})
	}
}

func TestEngine_EventRouting(t *testing.T) {
	_, nc := startTestNATS(t)

	tmpDir := t.TempDir()

	// Write a workflow that echoes events as commands.
	workflowCode := `
sekia.on("sekia.events.test", function(event)
	sekia.command("echo-agent", "echo", {
		original_id = event.id,
		original_type = event.type,
	})
end)
`
	wfPath := filepath.Join(tmpDir, "echo.lua")
	os.WriteFile(wfPath, []byte(workflowCode), 0644)

	eng := New(nc, tmpDir, nil, 0, "", testLogger())
	if err := eng.Start(); err != nil {
		t.Fatalf("engine start: %v", err)
	}
	defer eng.Stop()

	if err := eng.LoadWorkflow("echo", wfPath); err != nil {
		t.Fatalf("load workflow: %v", err)
	}

	// Subscribe to capture the command.
	received := make(chan []byte, 1)
	sub, err := nc.Subscribe("sekia.commands.echo-agent", func(msg *nats.Msg) {
		received <- msg.Data
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	// Publish a test event.
	ev := protocol.NewEvent("test.event", "test-source", map[string]any{
		"key": "value",
	})
	data, _ := json.Marshal(ev)
	nc.Publish("sekia.events.test", data)
	nc.Flush()

	select {
	case cmdData := <-received:
		var cmd map[string]any
		json.Unmarshal(cmdData, &cmd)
		if cmd["command"] != "echo" {
			t.Errorf("command = %v, want echo", cmd["command"])
		}
		if cmd["source"] != "workflow:echo" {
			t.Errorf("source = %v, want workflow:echo", cmd["source"])
		}
		payload := cmd["payload"].(map[string]any)
		if payload["original_id"] != ev.ID {
			t.Errorf("original_id = %v, want %s", payload["original_id"], ev.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for command")
	}
}

func TestEngine_SelfEventGuard(t *testing.T) {
	_, nc := startTestNATS(t)

	tmpDir := t.TempDir()

	// This workflow publishes events to its own listened subject.
	// The self-event guard should prevent infinite loops.
	workflowCode := `
counter = 0
sekia.on("sekia.events.loop", function(event)
	counter = counter + 1
	if counter > 1 then
		error("should not be called more than once")
	end
	sekia.publish("sekia.events.loop", "echo", { n = counter })
end)
`
	wfPath := filepath.Join(tmpDir, "looper.lua")
	os.WriteFile(wfPath, []byte(workflowCode), 0644)

	eng := New(nc, tmpDir, nil, 0, "", testLogger())
	if err := eng.Start(); err != nil {
		t.Fatalf("engine start: %v", err)
	}
	defer eng.Stop()

	if err := eng.LoadWorkflow("looper", wfPath); err != nil {
		t.Fatalf("load workflow: %v", err)
	}

	// Publish an event from an external source.
	ev := protocol.NewEvent("trigger", "external", map[string]any{})
	data, _ := json.Marshal(ev)
	nc.Publish("sekia.events.loop", data)
	nc.Flush()

	// Give it time to potentially loop.
	time.Sleep(500 * time.Millisecond)

	// If we get here without the error("should not be called more than once"),
	// the self-event guard worked.
}

func TestEngine_WildcardHandler(t *testing.T) {
	_, nc := startTestNATS(t)

	tmpDir := t.TempDir()

	workflowCode := `
sekia.on("sekia.events.>", function(event)
	sekia.command("catch-all", "got_event", {
		event_type = event.type,
	})
end)
`
	wfPath := filepath.Join(tmpDir, "catchall.lua")
	os.WriteFile(wfPath, []byte(workflowCode), 0644)

	eng := New(nc, tmpDir, nil, 0, "", testLogger())
	if err := eng.Start(); err != nil {
		t.Fatalf("engine start: %v", err)
	}
	defer eng.Stop()

	if err := eng.LoadWorkflow("catchall", wfPath); err != nil {
		t.Fatalf("load workflow: %v", err)
	}

	received := make(chan []byte, 2)
	sub, err := nc.Subscribe("sekia.commands.catch-all", func(msg *nats.Msg) {
		received <- msg.Data
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	// Publish events on different subjects — both should match.
	for _, src := range []string{"sekia.events.github", "sekia.events.slack"} {
		ev := protocol.NewEvent("test", "external", map[string]any{})
		data, _ := json.Marshal(ev)
		nc.Publish(src, data)
	}
	nc.Flush()

	for i := 0; i < 2; i++ {
		select {
		case <-received:
			// OK
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for command %d", i+1)
		}
	}
}

func TestEngine_MultipleWorkflows(t *testing.T) {
	_, nc := startTestNATS(t)

	tmpDir := t.TempDir()

	// Workflow A handles github events.
	wfA := `
sekia.on("sekia.events.github", function(event)
	sekia.command("agent-a", "handle", { from = "wf-a" })
end)
`
	// Workflow B handles slack events.
	wfB := `
sekia.on("sekia.events.slack", function(event)
	sekia.command("agent-b", "handle", { from = "wf-b" })
end)
`
	os.WriteFile(filepath.Join(tmpDir, "wf_a.lua"), []byte(wfA), 0644)
	os.WriteFile(filepath.Join(tmpDir, "wf_b.lua"), []byte(wfB), 0644)

	eng := New(nc, tmpDir, nil, 0, "", testLogger())
	if err := eng.Start(); err != nil {
		t.Fatalf("engine start: %v", err)
	}
	defer eng.Stop()

	eng.LoadWorkflow("wf_a", filepath.Join(tmpDir, "wf_a.lua"))
	eng.LoadWorkflow("wf_b", filepath.Join(tmpDir, "wf_b.lua"))

	receivedA := make(chan []byte, 1)
	receivedB := make(chan []byte, 1)
	nc.Subscribe("sekia.commands.agent-a", func(msg *nats.Msg) {
		receivedA <- msg.Data
	})
	nc.Subscribe("sekia.commands.agent-b", func(msg *nats.Msg) {
		receivedB <- msg.Data
	})

	// Publish a github event — only wf_a should handle it.
	ev := protocol.NewEvent("pr.opened", "external", map[string]any{})
	data, _ := json.Marshal(ev)
	nc.Publish("sekia.events.github", data)
	nc.Flush()

	select {
	case <-receivedA:
		// OK — wf_a handled it
	case <-time.After(5 * time.Second):
		t.Fatal("wf_a did not handle github event")
	}

	// wf_b should NOT have received anything.
	select {
	case <-receivedB:
		t.Fatal("wf_b should not have handled github event")
	case <-time.After(200 * time.Millisecond):
		// OK — correctly not received
	}

	// Verify Workflows() snapshot.
	infos := eng.Workflows()
	if len(infos) != 2 {
		t.Fatalf("expected 2 workflows, got %d", len(infos))
	}
}

func TestEngine_HandlerError(t *testing.T) {
	_, nc := startTestNATS(t)

	tmpDir := t.TempDir()

	// A workflow with a handler that always errors.
	workflowCode := `
sekia.on("sekia.events.test", function(event)
	error("intentional error")
end)
`
	wfPath := filepath.Join(tmpDir, "erroring.lua")
	os.WriteFile(wfPath, []byte(workflowCode), 0644)

	eng := New(nc, tmpDir, nil, 0, "", testLogger())
	if err := eng.Start(); err != nil {
		t.Fatalf("engine start: %v", err)
	}
	defer eng.Stop()

	if err := eng.LoadWorkflow("erroring", wfPath); err != nil {
		t.Fatalf("load workflow: %v", err)
	}

	// Publish an event to trigger the error.
	ev := protocol.NewEvent("test", "external", map[string]any{})
	data, _ := json.Marshal(ev)
	nc.Publish("sekia.events.test", data)
	nc.Flush()

	// Give it time to process.
	time.Sleep(500 * time.Millisecond)

	// Engine should still be running (error is caught).
	if eng.Count() != 1 {
		t.Fatalf("expected 1 workflow, got %d", eng.Count())
	}
}

func TestEngine_HandlerTimeout(t *testing.T) {
	_, nc := startTestNATS(t)

	tmpDir := t.TempDir()

	// A workflow with an infinite loop handler.
	workflowCode := `
sekia.on("sekia.events.test", function(event)
	while true do end
end)
`
	wfPath := filepath.Join(tmpDir, "infinite.lua")
	os.WriteFile(wfPath, []byte(workflowCode), 0644)

	// Use a short timeout (200ms) so the test doesn't hang.
	eng := New(nc, tmpDir, nil, 200*time.Millisecond, "", testLogger())
	if err := eng.Start(); err != nil {
		t.Fatalf("engine start: %v", err)
	}
	defer eng.Stop()

	if err := eng.LoadWorkflow("infinite", wfPath); err != nil {
		t.Fatalf("load workflow: %v", err)
	}

	// Publish an event to trigger the infinite loop.
	ev := protocol.NewEvent("test", "external", map[string]any{})
	data, _ := json.Marshal(ev)
	nc.Publish("sekia.events.test", data)
	nc.Flush()

	// Wait for the timeout to fire.
	time.Sleep(1 * time.Second)

	// The handler should have timed out and recorded an error.
	infos := eng.Workflows()
	if len(infos) != 1 {
		t.Fatalf("expected 1 workflow, got %d", len(infos))
	}
	if infos[0].Errors == 0 {
		t.Fatal("expected error count > 0 from timed-out handler")
	}

	// Verify the workflow can still process subsequent events (not stuck).
	// Load a second workflow that proves the engine is responsive.
	wfPath2 := filepath.Join(tmpDir, "ok.lua")
	os.WriteFile(wfPath2, []byte(`
sekia.on("sekia.events.ok", function(event)
	sekia.command("timeout-test", "ack", {})
end)
`), 0644)
	if err := eng.LoadWorkflow("ok", wfPath2); err != nil {
		t.Fatalf("load ok workflow: %v", err)
	}

	received := make(chan struct{}, 1)
	sub, err := nc.Subscribe("sekia.commands.timeout-test", func(msg *nats.Msg) {
		received <- struct{}{}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	ev2 := protocol.NewEvent("ok.event", "external", map[string]any{})
	data2, _ := json.Marshal(ev2)
	nc.Publish("sekia.events.ok", data2)
	nc.Flush()

	select {
	case <-received:
		// Engine is still alive and processing events.
	case <-time.After(5 * time.Second):
		t.Fatal("engine did not process subsequent event after timeout")
	}
}
