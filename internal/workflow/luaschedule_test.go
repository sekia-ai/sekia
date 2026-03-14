package workflow

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestLuaSchedule_Registration(t *testing.T) {
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
		sekia.schedule(60, function()
			-- periodic task
		end)
		sekia.schedule(300, function()
			-- another periodic task
		end)
	`)
	if err != nil {
		t.Fatalf("DoString: %v", err)
	}

	if len(ctx.schedules) != 2 {
		t.Fatalf("expected 2 schedules, got %d", len(ctx.schedules))
	}
	if ctx.schedules[0].Interval != 60*time.Second {
		t.Errorf("schedule[0].Interval = %v, want 60s", ctx.schedules[0].Interval)
	}
	if ctx.schedules[1].Interval != 300*time.Second {
		t.Errorf("schedule[1].Interval = %v, want 300s", ctx.schedules[1].Interval)
	}
}

func TestLuaSchedule_MinInterval(t *testing.T) {
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
		sekia.schedule(0.5, function() end)
	`)
	if err == nil {
		t.Fatal("expected error for interval < 1 second")
	}
}

func TestEngine_ScheduleFires(t *testing.T) {
	_, nc := startTestNATS(t)

	tmpDir := t.TempDir()

	// Write a workflow that uses sekia.schedule to publish an event.
	workflowCode := `
sekia.schedule(1, function()
	sekia.publish("sekia.events.scheduled", "schedule.tick", {
		source_wf = sekia.name,
	})
end)
`
	if err := os.WriteFile(filepath.Join(tmpDir, "schedule-test.lua"), []byte(workflowCode), 0644); err != nil {
		t.Fatal(err)
	}

	eng := New(nc, tmpDir, nil, 0, "", testLogger())
	if err := eng.Start(); err != nil {
		t.Fatal(err)
	}
	defer eng.Stop()

	// Subscribe to capture scheduled events.
	received := make(chan struct{}, 5)
	sub, err := nc.Subscribe("sekia.events.scheduled", func(msg *nats.Msg) {
		received <- struct{}{}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Unsubscribe()

	if err := eng.LoadWorkflow("schedule-test", filepath.Join(tmpDir, "schedule-test.lua")); err != nil {
		t.Fatal(err)
	}

	// Wait for at least one scheduled tick.
	select {
	case <-received:
		// success
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for scheduled event")
	}
}
