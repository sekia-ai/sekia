package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	lua "github.com/yuin/gopher-lua"

	"github.com/sekia-ai/sekia/internal/ai"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

// WorkflowInfo describes a loaded workflow for the API.
type WorkflowInfo struct {
	Name     string   `json:"name"`
	FilePath string   `json:"file_path"`
	Handlers int      `json:"handlers"`
	Patterns []string `json:"patterns"`
	LoadedAt time.Time `json:"loaded_at"`
	Events   int64    `json:"events"`
	Errors   int64    `json:"errors"`
}

// workflowState tracks a loaded workflow and its isolated Lua VM.
type workflowState struct {
	name           string
	filePath       string
	L              *lua.LState
	modCtx         *moduleContext
	loadedAt       time.Time
	events         atomic.Int64
	errors         atomic.Int64
	handlerTimeout time.Duration

	eventCh chan *nats.Msg
	done    chan struct{}
}

// Engine manages Lua workflow scripts and routes NATS events to Lua handlers.
type Engine struct {
	mu              sync.RWMutex
	workflows       map[string]*workflowState
	nc              *nats.Conn
	logger          zerolog.Logger
	dir             string
	sub             *nats.Subscription
	llm             ai.LLMClient
	handlerTimeout  time.Duration
	commandSecret   string
	verifyIntegrity bool
}

// New creates a workflow engine. Does not start it.
// The llm parameter is optional (may be nil if AI is not configured).
// handlerTimeout limits how long a single Lua handler call may run (0 = no limit).
// commandSecret is used for HMAC-SHA256 signing of outgoing commands (empty = no signing).
func New(nc *nats.Conn, dir string, llm ai.LLMClient, handlerTimeout time.Duration, commandSecret string, logger zerolog.Logger) *Engine {
	return &Engine{
		workflows:      make(map[string]*workflowState),
		nc:             nc,
		logger:         logger.With().Str("component", "workflow").Logger(),
		dir:            dir,
		llm:            llm,
		handlerTimeout: handlerTimeout,
		commandSecret:  commandSecret,
	}
}

// Start subscribes to NATS events. Workflow loading is handled separately by LoadDir.
func (e *Engine) Start() error {
	sub, err := e.nc.Subscribe("sekia.events.>", e.handleEvent)
	if err != nil {
		return fmt.Errorf("subscribe to events: %w", err)
	}
	e.sub = sub

	e.logger.Info().Str("dir", e.dir).Msg("workflow engine started")
	return nil
}

// Stop unsubscribes from NATS, stops all workflow goroutines, and closes all LStates.
func (e *Engine) Stop() {
	if e.sub != nil {
		e.sub.Unsubscribe()
	}

	// Atomically collect and clear — stop outside the lock to avoid
	// blocking handleEvent while goroutines drain their channels.
	e.mu.Lock()
	old := e.workflows
	e.workflows = make(map[string]*workflowState)
	e.mu.Unlock()

	for _, ws := range old {
		e.stopWorkflow(ws)
	}

	e.logger.Info().Msg("workflow engine stopped")
}

// Workflows returns a snapshot of all loaded workflows.
func (e *Engine) Workflows() []WorkflowInfo {
	e.mu.RLock()
	defer e.mu.RUnlock()

	infos := make([]WorkflowInfo, 0, len(e.workflows))
	for _, ws := range e.workflows {
		patterns := make([]string, len(ws.modCtx.handlers))
		for i, h := range ws.modCtx.handlers {
			patterns[i] = h.Pattern
		}
		infos = append(infos, WorkflowInfo{
			Name:     ws.name,
			FilePath: ws.filePath,
			Handlers: len(ws.modCtx.handlers),
			Patterns: patterns,
			LoadedAt: ws.loadedAt,
			Events:   ws.events.Load(),
			Errors:   ws.errors.Load(),
		})
	}
	return infos
}

// Count returns the number of loaded workflows.
func (e *Engine) Count() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.workflows)
}

// SetHandlerTimeout updates the handler timeout for all future workflow loads.
func (e *Engine) SetHandlerTimeout(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handlerTimeout = d
}

// SetLLMClient updates the LLM client for all future workflow loads.
func (e *Engine) SetLLMClient(llm ai.LLMClient) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.llm = llm
}

// SetVerifyIntegrity enables or disables SHA256 manifest verification for workflow loading.
func (e *Engine) SetVerifyIntegrity(v bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.verifyIntegrity = v
}

// LoadWorkflow loads a single Lua file as a workflow.
func (e *Engine) LoadWorkflow(name, filePath string) error {
	wfLogger := e.logger.With().Str("workflow", name).Logger()

	if e.verifyIntegrity {
		manifest, err := LoadManifest(e.dir)
		if err != nil {
			return fmt.Errorf("load manifest: %w", err)
		}
		if manifest == nil {
			return fmt.Errorf("integrity verification enabled but %s not found in %s", ManifestFilename, e.dir)
		}
		filename := filepath.Base(filePath)
		if err := manifest.Verify(filename, filePath); err != nil {
			return fmt.Errorf("integrity check failed: %w", err)
		}
		wfLogger.Debug().Msg("integrity check passed")
	}

	L := NewSandboxedState(name, wfLogger)
	modCtx := &moduleContext{
		name:          name,
		nc:            e.nc,
		logger:        wfLogger,
		llm:           e.llm,
		commandSecret: e.commandSecret,
	}
	registerSekiaModule(L, modCtx)

	if err := L.DoFile(filePath); err != nil {
		L.Close()
		return fmt.Errorf("load %s: %w", filePath, err)
	}

	ws := &workflowState{
		name:           name,
		filePath:       filePath,
		L:              L,
		modCtx:         modCtx,
		loadedAt:       time.Now(),
		handlerTimeout: e.handlerTimeout,
		eventCh:        make(chan *nats.Msg, 4096),
		done:           make(chan struct{}),
	}

	go ws.run()

	// Atomically swap the map entry — stop old workflow OUTSIDE the lock
	// to avoid blocking handleEvent while the goroutine drains its channel.
	e.mu.Lock()
	old := e.workflows[name]
	e.workflows[name] = ws
	e.mu.Unlock()

	if old != nil {
		e.stopWorkflow(old)
	}

	wfLogger.Info().
		Int("handlers", len(modCtx.handlers)).
		Msg("loaded workflow")

	return nil
}

// UnloadWorkflow stops and removes a workflow by name.
func (e *Engine) UnloadWorkflow(name string) {
	e.mu.Lock()
	ws, ok := e.workflows[name]
	if ok {
		delete(e.workflows, name)
	}
	e.mu.Unlock()

	if ok {
		e.stopWorkflow(ws)
		e.logger.Info().Str("workflow", name).Msg("unloaded workflow")
	}
}

// handleEvent is the NATS callback for sekia.events.>. It routes events to matching workflows.
func (e *Engine) handleEvent(msg *nats.Msg) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	routed := false
	for _, ws := range e.workflows {
		// Self-event guard: skip events published by this workflow.
		source := extractSource(msg.Data)
		if source == fmt.Sprintf("workflow:%s", ws.name) {
			continue
		}

		// Check if any handler matches this subject.
		matched := false
		for _, h := range ws.modCtx.handlers {
			if SubjectMatches(h.Pattern, msg.Subject) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}

		// Non-blocking send to the workflow's event channel.
		select {
		case ws.eventCh <- msg:
			routed = true
			e.logger.Debug().
				Str("workflow", ws.name).
				Str("subject", msg.Subject).
				Msg("routed event to workflow")
		default:
			ws.errors.Add(1)
			e.logger.Warn().
				Str("workflow", ws.name).
				Str("subject", msg.Subject).
				Msg("event channel full, dropping event")
		}
	}

	if !routed {
		e.logger.Debug().
			Str("subject", msg.Subject).
			Int("workflows", len(e.workflows)).
			Msg("event matched no workflows")
	}
}

// run is the per-workflow goroutine that processes events sequentially.
func (ws *workflowState) run() {
	defer close(ws.done)

	for msg := range ws.eventCh {
		var ev protocol.Event
		if err := json.Unmarshal(msg.Data, &ev); err != nil {
			ws.errors.Add(1)
			ws.modCtx.logger.Error().Err(err).Msg("unmarshal event")
			continue
		}

		ws.modCtx.logger.Debug().
			Str("event_type", ev.Type).
			Str("event_id", ev.ID).
			Str("subject", msg.Subject).
			Msg("processing event")

		eventTable := EventToLua(ws.L, ev)

		for _, h := range ws.modCtx.handlers {
			if !SubjectMatches(h.Pattern, msg.Subject) {
				continue
			}
			ws.callHandler(h, ev.ID, eventTable)
		}
		ws.events.Add(1)
	}
}

// callHandler invokes a single Lua handler with an optional execution timeout.
func (ws *workflowState) callHandler(h handlerEntry, eventID string, eventTable *lua.LTable) {
	var cancel context.CancelFunc
	if ws.handlerTimeout > 0 {
		var ctx context.Context
		ctx, cancel = context.WithTimeout(context.Background(), ws.handlerTimeout)
		ws.L.SetContext(ctx)
	}

	err := ws.L.CallByParam(lua.P{
		Fn:      h.Fn,
		NRet:    0,
		Protect: true,
	}, eventTable)

	if cancel != nil {
		timedOut := ws.L.Context() != nil && ws.L.Context().Err() != nil
		cancel()
		ws.L.SetContext(nil)
		if err != nil && timedOut {
			ws.errors.Add(1)
			ws.modCtx.logger.Error().
				Dur("timeout", ws.handlerTimeout).
				Str("pattern", h.Pattern).
				Str("event_id", eventID).
				Msg("handler timed out")
			return
		}
	}

	if err != nil {
		ws.errors.Add(1)
		ws.modCtx.logger.Error().
			Err(err).
			Str("pattern", h.Pattern).
			Str("event_id", eventID).
			Msg("handler error")
	}
}

// stopWorkflow closes the event channel and waits for the goroutine to finish, then closes the LState.
func (e *Engine) stopWorkflow(ws *workflowState) {
	close(ws.eventCh)
	<-ws.done
	ws.L.Close()
}

// extractSource does a lightweight parse of the JSON "source" field without full unmarshal.
func extractSource(data []byte) string {
	var partial struct {
		Source string `json:"source"`
	}
	json.Unmarshal(data, &partial)
	return partial.Source
}

// SubjectMatches implements NATS-style subject matching.
// Patterns use '.' as delimiter, '*' matches a single token, '>' matches the rest.
func SubjectMatches(pattern, subject string) bool {
	patternParts := strings.Split(pattern, ".")
	subjectParts := strings.Split(subject, ".")

	for i, pp := range patternParts {
		if pp == ">" {
			return true // '>' matches everything remaining
		}
		if i >= len(subjectParts) {
			return false // pattern has more tokens than subject
		}
		if pp != "*" && pp != subjectParts[i] {
			return false // token mismatch
		}
	}

	return len(patternParts) == len(subjectParts)
}
