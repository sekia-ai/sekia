package workflow

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDir(t *testing.T) {
	_, nc := startTestNATS(t)

	tmpDir := t.TempDir()
	wfDir := filepath.Join(tmpDir, "workflows")
	os.MkdirAll(wfDir, 0755)

	// Write two valid workflows and one non-lua file.
	os.WriteFile(filepath.Join(wfDir, "wf_a.lua"), []byte(`
		sekia.on("sekia.events.a", function(event) end)
	`), 0644)
	os.WriteFile(filepath.Join(wfDir, "wf_b.lua"), []byte(`
		sekia.on("sekia.events.b", function(event) end)
	`), 0644)
	os.WriteFile(filepath.Join(wfDir, "readme.txt"), []byte("not a workflow"), 0644)

	eng := New(nc, wfDir, nil, 0, testLogger())
	if err := eng.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer eng.Stop()

	if err := eng.LoadDir(); err != nil {
		t.Fatalf("load dir: %v", err)
	}

	if eng.Count() != 2 {
		t.Fatalf("expected 2 workflows, got %d", eng.Count())
	}

	// Verify workflow names.
	infos := eng.Workflows()
	names := make(map[string]bool)
	for _, info := range infos {
		names[info.Name] = true
	}
	if !names["wf_a"] || !names["wf_b"] {
		t.Errorf("expected wf_a and wf_b, got %v", names)
	}
}

func TestLoadDir_EmptyDir(t *testing.T) {
	_, nc := startTestNATS(t)

	tmpDir := t.TempDir()
	wfDir := filepath.Join(tmpDir, "workflows")
	// Don't create it — LoadDir should create it.

	eng := New(nc, wfDir, nil, 0, testLogger())
	if err := eng.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer eng.Stop()

	if err := eng.LoadDir(); err != nil {
		t.Fatalf("load dir: %v", err)
	}

	if eng.Count() != 0 {
		t.Fatalf("expected 0 workflows, got %d", eng.Count())
	}

	// Verify dir was created.
	if _, err := os.Stat(wfDir); os.IsNotExist(err) {
		t.Fatal("expected workflow dir to be created")
	}
}

func TestLoadDir_SyntaxError(t *testing.T) {
	_, nc := startTestNATS(t)

	tmpDir := t.TempDir()
	wfDir := filepath.Join(tmpDir, "workflows")
	os.MkdirAll(wfDir, 0755)

	// One valid, one with syntax error.
	os.WriteFile(filepath.Join(wfDir, "good.lua"), []byte(`
		sekia.on("sekia.events.test", function(event) end)
	`), 0644)
	os.WriteFile(filepath.Join(wfDir, "bad.lua"), []byte(`
		this is not valid lua !@#$
	`), 0644)

	eng := New(nc, wfDir, nil, 0, testLogger())
	if err := eng.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer eng.Stop()

	if err := eng.LoadDir(); err != nil {
		t.Fatalf("load dir: %v", err)
	}

	// Only the good workflow should be loaded.
	if eng.Count() != 1 {
		t.Fatalf("expected 1 workflow, got %d", eng.Count())
	}
}

func TestReloadAll(t *testing.T) {
	_, nc := startTestNATS(t)

	tmpDir := t.TempDir()
	wfDir := filepath.Join(tmpDir, "workflows")
	os.MkdirAll(wfDir, 0755)

	os.WriteFile(filepath.Join(wfDir, "wf.lua"), []byte(`
		sekia.on("sekia.events.v1", function(event) end)
	`), 0644)

	eng := New(nc, wfDir, nil, 0, testLogger())
	if err := eng.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer eng.Stop()

	eng.LoadDir()
	if eng.Count() != 1 {
		t.Fatalf("expected 1 workflow, got %d", eng.Count())
	}

	// Modify the workflow file.
	os.WriteFile(filepath.Join(wfDir, "wf.lua"), []byte(`
		sekia.on("sekia.events.v2", function(event) end)
		sekia.on("sekia.events.v2b", function(event) end)
	`), 0644)

	// Add a second workflow.
	os.WriteFile(filepath.Join(wfDir, "wf2.lua"), []byte(`
		sekia.on("sekia.events.extra", function(event) end)
	`), 0644)

	eng.ReloadAll()
	if eng.Count() != 2 {
		t.Fatalf("expected 2 workflows after reload, got %d", eng.Count())
	}

	// Verify the first workflow was reloaded with new handlers.
	for _, info := range eng.Workflows() {
		if info.Name == "wf" {
			if info.Handlers != 2 {
				t.Errorf("wf handlers = %d, want 2", info.Handlers)
			}
		}
	}
}

func TestHotReload(t *testing.T) {
	_, nc := startTestNATS(t)

	tmpDir := t.TempDir()
	wfDir := filepath.Join(tmpDir, "workflows")
	os.MkdirAll(wfDir, 0755)

	eng := New(nc, wfDir, nil, 0, testLogger())
	if err := eng.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer eng.Stop()

	eng.LoadDir()

	if err := eng.StartWatcher(); err != nil {
		t.Fatalf("start watcher: %v", err)
	}

	// Create a new workflow file.
	os.WriteFile(filepath.Join(wfDir, "hot.lua"), []byte(`
		sekia.on("sekia.events.hot", function(event) end)
	`), 0644)

	// Wait for debounce + processing.
	time.Sleep(2 * time.Second)

	if eng.Count() != 1 {
		t.Fatalf("expected 1 workflow after hot reload, got %d", eng.Count())
	}
}
