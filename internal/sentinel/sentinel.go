package sentinel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"

	"github.com/sekia-ai/sekia/internal/ai"
	"github.com/sekia-ai/sekia/internal/registry"
	"github.com/sekia-ai/sekia/internal/workflow"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

// Config holds sentinel settings from the [sentinel] section of sekia.toml.
type Config struct {
	Enabled       bool          `mapstructure:"enabled"`
	Interval      time.Duration `mapstructure:"interval"`
	ChecklistPath string        `mapstructure:"checklist_path"`
}

// Sentinel performs AI-driven proactive checks on a schedule.
type Sentinel struct {
	cfg      Config
	llm      ai.LLMClient
	nc       *nats.Conn
	registry *registry.Registry
	engine   *workflow.Engine
	logger   zerolog.Logger

	mu     sync.Mutex
	cancel context.CancelFunc
}

// New creates a Sentinel from the given dependencies.
func New(cfg Config, llm ai.LLMClient, nc *nats.Conn, reg *registry.Registry, eng *workflow.Engine, logger zerolog.Logger) *Sentinel {
	return &Sentinel{
		cfg:      cfg,
		llm:      llm,
		nc:       nc,
		registry: reg,
		engine:   eng,
		logger:   logger.With().Str("component", "sentinel").Logger(),
	}
}

// Start begins the sentinel check loop in a background goroutine.
func (s *Sentinel) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()

	s.logger.Info().
		Dur("interval", s.cfg.Interval).
		Str("checklist", s.cfg.ChecklistPath).
		Msg("sentinel started")

	go s.run(ctx)
}

// Stop gracefully stops the sentinel loop.
func (s *Sentinel) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

func (s *Sentinel) run(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Sentinel) tick(ctx context.Context) {
	checklist, err := loadChecklist(s.cfg.ChecklistPath)
	if err != nil {
		s.logger.Warn().Err(err).Msg("failed to read sentinel checklist")
		return
	}
	if checklist == "" {
		return
	}

	systemContext := s.gatherContext()

	prompt := fmt.Sprintf(`You are a proactive sentinel monitoring a multi-agent system.

## Current System State
%s

## Checklist
%s

## Instructions
Review each checklist item against the current system state. For each item that needs attention, include it in the actions array. If nothing needs attention, return an empty actions array.

Respond with JSON only:
{"actions": [{"type": "sentinel.action.required", "reasoning": "...", "checklist_item": "..."}]}

If nothing needs attention:
{"actions": []}`, systemContext, checklist)

	callCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	result, err := s.llm.Complete(callCtx, ai.CompleteRequest{
		Prompt:   prompt,
		JSONMode: true,
	})
	if err != nil {
		s.logger.Error().Err(err).Msg("sentinel AI call failed")
		return
	}

	s.processResult(result)
}

func (s *Sentinel) gatherContext() string {
	var b strings.Builder

	agents := s.registry.Agents()
	fmt.Fprintf(&b, "### Agents (%d connected)\n", len(agents))
	for _, a := range agents {
		fmt.Fprintf(&b, "- %s (status=%s, events=%d, errors=%d, last_heartbeat=%s)\n",
			a.Name, a.Status, a.EventsProcessed, a.Errors, a.LastHeartbeat.Format(time.RFC3339))
	}

	if s.engine != nil {
		workflows := s.engine.Workflows()
		fmt.Fprintf(&b, "\n### Workflows (%d loaded)\n", len(workflows))
		for _, w := range workflows {
			fmt.Fprintf(&b, "- %s (handlers=%d, events=%d, errors=%d)\n",
				w.Name, w.Handlers, w.Events, w.Errors)
		}
	}

	return b.String()
}

// sentinelResponse is the expected JSON structure from the AI.
type sentinelResponse struct {
	Actions []sentinelAction `json:"actions"`
}

type sentinelAction struct {
	Type          string `json:"type"`
	Reasoning     string `json:"reasoning"`
	ChecklistItem string `json:"checklist_item"`
}

func (s *Sentinel) processResult(result string) {
	var resp sentinelResponse
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		s.logger.Warn().Err(err).Str("result", result).Msg("failed to parse sentinel response")
		return
	}

	if len(resp.Actions) == 0 {
		s.logger.Debug().Msg("SENTINEL_OK")
		return
	}

	for _, action := range resp.Actions {
		eventType := action.Type
		if eventType == "" {
			eventType = "sentinel.action.required"
		}
		evt := protocol.NewEvent(eventType, "sentinel", map[string]any{
			"reasoning":      action.Reasoning,
			"checklist_item": action.ChecklistItem,
		})
		data, err := json.Marshal(evt)
		if err != nil {
			s.logger.Error().Err(err).Msg("failed to marshal sentinel event")
			continue
		}
		if err := s.nc.Publish("sekia.events.sentinel", data); err != nil {
			s.logger.Error().Err(err).Msg("failed to publish sentinel event")
			continue
		}
		s.logger.Info().
			Str("type", eventType).
			Str("item", action.ChecklistItem).
			Msg("sentinel action published")
	}
}

func loadChecklist(path string) (string, error) {
	if path == "" {
		return "", nil
	}

	cleanPath := filepath.Clean(path)
	dir := filepath.Dir(cleanPath)
	base := filepath.Base(cleanPath)

	root, err := os.OpenRoot(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("open sentinel checklist directory: %w", err)
	}
	defer root.Close()

	f, err := root.Open(base)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read sentinel checklist: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("read sentinel checklist: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}
