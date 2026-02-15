package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

// Config holds connection options for an agent.
type Config struct {
	NATSUrl  string
	NATSOpts []nats.Option
}

// Agent is the base for all sekia agents.
type Agent struct {
	Name         string
	Version      string
	Capabilities []string
	Commands     []string

	nc     *nats.Conn
	logger zerolog.Logger
	cancel context.CancelFunc

	eventsProcessed atomic.Int64
	errors          atomic.Int64
	lastEvent       atomic.Value // stores time.Time
}

// New creates an Agent, connects to NATS, registers, and starts heartbeating.
func New(cfg Config, name, version string, capabilities, commands []string, logger zerolog.Logger) (*Agent, error) {
	agentLogger := logger.With().Str("agent", name).Logger()

	// Resilience: infinite reconnect with logging on state changes.
	resilienceOpts := []nats.Option{
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				agentLogger.Warn().Err(err).Msg("NATS disconnected")
			}
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			agentLogger.Info().Msg("NATS reconnected")
		}),
		nats.ClosedHandler(func(_ *nats.Conn) {
			agentLogger.Warn().Msg("NATS connection closed")
		}),
	}

	opts := append(resilienceOpts, cfg.NATSOpts...)
	nc, err := nats.Connect(cfg.NATSUrl, opts...)
	if err != nil {
		return nil, err
	}

	a := &Agent{
		Name:         name,
		Version:      version,
		Capabilities: capabilities,
		Commands:     commands,
		nc:           nc,
		logger:       agentLogger,
	}
	a.lastEvent.Store(time.Time{})

	if err := a.register(); err != nil {
		nc.Close()
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	go a.heartbeatLoop(ctx)

	return a, nil
}

func (a *Agent) register() error {
	reg := protocol.Registration{
		Name:         a.Name,
		Version:      a.Version,
		Capabilities: a.Capabilities,
		Commands:     a.Commands,
	}
	data, err := json.Marshal(reg)
	if err != nil {
		return err
	}
	return a.nc.Publish(protocol.SubjectRegistry, data)
}

func (a *Agent) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Send an initial heartbeat immediately.
	a.sendHeartbeat()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.sendHeartbeat()
		}
	}
}

func (a *Agent) sendHeartbeat() {
	hb := protocol.Heartbeat{
		Name:            a.Name,
		Status:          "running",
		LastEvent:       a.lastEvent.Load().(time.Time),
		EventsProcessed: a.eventsProcessed.Load(),
		Errors:          a.errors.Load(),
	}
	data, _ := json.Marshal(hb)
	if err := a.nc.Publish(protocol.SubjectHeartbeat(a.Name), data); err != nil {
		a.logger.Error().Err(err).Msg("failed to send heartbeat")
	}
}

// Conn returns the underlying NATS connection for custom subscriptions.
func (a *Agent) Conn() *nats.Conn { return a.nc }

// RecordEvent increments counters after processing an event.
func (a *Agent) RecordEvent() {
	a.eventsProcessed.Add(1)
	a.lastEvent.Store(time.Now())
}

// RecordError increments the error counter.
func (a *Agent) RecordError() {
	a.errors.Add(1)
}

// OnConfigReload registers a callback invoked when a config reload message
// arrives via NATS (broadcast or agent-targeted). Must be called after New().
func (a *Agent) OnConfigReload(fn func()) error {
	if _, err := a.nc.Subscribe(protocol.SubjectConfigReload, func(_ *nats.Msg) {
		a.logger.Info().Msg("config reload requested (broadcast)")
		fn()
	}); err != nil {
		return fmt.Errorf("subscribe config reload broadcast: %w", err)
	}

	if _, err := a.nc.Subscribe(protocol.SubjectConfigReloadAgent(a.Name), func(_ *nats.Msg) {
		a.logger.Info().Msg("config reload requested (targeted)")
		fn()
	}); err != nil {
		return fmt.Errorf("subscribe config reload agent: %w", err)
	}

	return nil
}

// Close stops heartbeating and disconnects.
func (a *Agent) Close() {
	if a.cancel != nil {
		a.cancel()
	}
	a.nc.Drain()
}
