package workflow

import (
	"encoding/json"
	"fmt"
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
	name     string
	filePath string
	L        *lua.LState
	modCtx   *moduleContext
	loadedAt time.Time
	events   atomic.Int64
	errors   atomic.Int64

	eventCh chan *nats.Msg
	done    chan struct{}
}

// Engine manages Lua workflow scripts and routes NATS events to Lua handlers.
type Engine struct {
	mu        sync.RWMutex
	workflows map[string]*workflowState
	nc        *nats.Conn
	logger    zerolog.Logger
	dir       string
	sub       *nats.Subscription
	llm       ai.LLMClient
}

// New creates a workflow engine. Does not start it.
// The llm parameter is optional (may be nil if AI is not configured).
func New(nc *nats.Conn, dir string, llm ai.LLMClient, logger zerolog.Logger) *Engine {
	return &Engine{
		workflows: make(map[string]*workflowState),
		nc:        nc,
		logger:    logger.With().Str("component", "workflow").Logger(),
		dir:       dir,
		llm:       llm,
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

	e.mu.Lock()
	defer e.mu.Unlock()

	for _, ws := range e.workflows {
		e.stopWorkflow(ws)
	}
	e.workflows = make(map[string]*workflowState)

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

// LoadWorkflow loads a single Lua file as a workflow.
func (e *Engine) LoadWorkflow(name, filePath string) error {
	wfLogger := e.logger.With().Str("workflow", name).Logger()

	L := NewSandboxedState(name, wfLogger)
	modCtx := &moduleContext{
		name:   name,
		nc:     e.nc,
		logger: wfLogger,
		llm:    e.llm,
	}
	registerSekiaModule(L, modCtx)

	if err := L.DoFile(filePath); err != nil {
		L.Close()
		return fmt.Errorf("load %s: %w", filePath, err)
	}

	ws := &workflowState{
		name:     name,
		filePath: filePath,
		L:        L,
		modCtx:   modCtx,
		loadedAt: time.Now(),
		eventCh:  make(chan *nats.Msg, 256),
		done:     make(chan struct{}),
	}

	go ws.run()

	e.mu.Lock()
	// Stop any existing workflow with the same name.
	if old, ok := e.workflows[name]; ok {
		e.stopWorkflow(old)
	}
	e.workflows[name] = ws
	e.mu.Unlock()

	wfLogger.Info().
		Int("handlers", len(modCtx.handlers)).
		Msg("loaded workflow")

	return nil
}

// UnloadWorkflow stops and removes a workflow by name.
func (e *Engine) UnloadWorkflow(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if ws, ok := e.workflows[name]; ok {
		e.stopWorkflow(ws)
		delete(e.workflows, name)
		e.logger.Info().Str("workflow", name).Msg("unloaded workflow")
	}
}

// handleEvent is the NATS callback for sekia.events.>. It routes events to matching workflows.
func (e *Engine) handleEvent(msg *nats.Msg) {
	e.mu.RLock()
	defer e.mu.RUnlock()

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
		default:
			ws.errors.Add(1)
			e.logger.Warn().
				Str("workflow", ws.name).
				Str("subject", msg.Subject).
				Msg("event channel full, dropping event")
		}
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

		eventTable := EventToLua(ws.L, ev)

		for _, h := range ws.modCtx.handlers {
			if !SubjectMatches(h.Pattern, msg.Subject) {
				continue
			}

			if err := ws.L.CallByParam(lua.P{
				Fn:      h.Fn,
				NRet:    0,
				Protect: true,
			}, eventTable); err != nil {
				ws.errors.Add(1)
				ws.modCtx.logger.Error().
					Err(err).
					Str("pattern", h.Pattern).
					Str("event_id", ev.ID).
					Msg("handler error")
			}
		}
		ws.events.Add(1)
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
