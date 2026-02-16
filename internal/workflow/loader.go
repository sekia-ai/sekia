package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// LoadDir scans the workflow directory and loads all .lua files.
func (e *Engine) LoadDir() error {
	if err := os.MkdirAll(e.dir, 0755); err != nil {
		return err
	}

	entries, err := os.ReadDir(e.dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".lua") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".lua")
		path := filepath.Join(e.dir, entry.Name())
		if err := e.LoadWorkflow(name, path); err != nil {
			e.logger.Error().Err(err).Str("file", entry.Name()).Msg("failed to load workflow")
			// Continue loading other workflows.
		}
	}

	return nil
}

// ReloadAll unloads all workflows and reloads from disk.
func (e *Engine) ReloadAll() error {
	// Atomically collect and clear — stop outside the lock to avoid
	// blocking handleEvent while goroutines drain their channels.
	e.mu.Lock()
	old := e.workflows
	e.workflows = make(map[string]*workflowState)
	e.mu.Unlock()

	for _, ws := range old {
		e.stopWorkflow(ws)
	}

	return e.LoadDir()
}

// StartWatcher starts an fsnotify watcher on the workflow directory.
// It debounces file changes and reloads affected workflows.
func (e *Engine) StartWatcher() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := watcher.Add(e.dir); err != nil {
		watcher.Close()
		return err
	}

	go e.watchLoop(watcher)

	e.logger.Info().Str("dir", e.dir).Msg("watching for workflow changes")
	return nil
}

func (e *Engine) watchLoop(watcher *fsnotify.Watcher) {
	defer watcher.Close()

	// Debounce: collect changed files over a 500ms window.
	var mu sync.Mutex
	pending := make(map[string]fsnotify.Op)
	var timer *time.Timer

	flush := func() {
		mu.Lock()
		batch := pending
		pending = make(map[string]fsnotify.Op)
		mu.Unlock()

		e.processBatch(batch)
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			mu.Lock()
			pending[event.Name] = event.Op
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(500*time.Millisecond, flush)
			mu.Unlock()

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			e.logger.Error().Err(err).Msg("watcher error")
		}
	}
}

// processBatch handles a debounced batch of file change events.
func (e *Engine) processBatch(batch map[string]fsnotify.Op) {
	// If the manifest file changed, do a full reload (re-verifies everything).
	manifestPath := filepath.Join(e.dir, ManifestFilename)
	for path := range batch {
		if path == manifestPath {
			e.logger.Info().Msg("manifest file changed, reloading all workflows")
			if err := e.ReloadAll(); err != nil {
				e.logger.Error().Err(err).Msg("failed to reload workflows after manifest change")
			}
			return
		}
	}

	for path, op := range batch {
		base := filepath.Base(path)
		if !strings.HasSuffix(base, ".lua") {
			continue
		}
		name := strings.TrimSuffix(base, ".lua")

		if op&(fsnotify.Remove|fsnotify.Rename) != 0 {
			e.UnloadWorkflow(name)
			continue
		}
		// Create or Write: (re)load the workflow.
		if err := e.LoadWorkflow(name, path); err != nil {
			e.logger.Error().Err(err).Str("file", base).Msg("failed to reload workflow")
		} else {
			e.logger.Info().Str("file", base).Msg("reloaded workflow")
		}
	}
}
