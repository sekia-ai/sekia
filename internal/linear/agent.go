package linear

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"

	"github.com/sekia-ai/sekia/pkg/agent"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

const (
	agentName    = "linear-agent"
	agentVersion = "0.0.20"
)

// LinearAgent bridges Linear events to the sekia event bus
// and executes Linear API commands dispatched by workflows.
type LinearAgent struct {
	cfg      Config
	lnClient LinearClient
	agent    *agent.Agent
	logger   zerolog.Logger
	stopCh   chan struct{}

	// Overridable for testing.
	natsOpts []nats.Option
}

// NewAgent creates a LinearAgent. Call Run() to start.
func NewAgent(cfg Config, logger zerolog.Logger) *LinearAgent {
	client := newRealLinearClient(cfg.Linear.APIKey)

	return &LinearAgent{
		cfg:      cfg,
		lnClient: client,
		logger:   logger.With().Str("component", "linear-agent").Logger(),
		stopCh:   make(chan struct{}),
	}
}

// Run starts the agent: connects to NATS, subscribes to commands,
// starts the poller, and blocks until signal or Stop().
func (la *LinearAgent) Run() error {
	// 1. Connect to NATS via the agent SDK.
	natsOpts := la.natsOpts
	if la.cfg.NATS.Token != "" {
		natsOpts = append(natsOpts, nats.Token(la.cfg.NATS.Token))
	}
	agentCfg := agent.Config{
		NATSUrl:  la.cfg.NATS.URL,
		NATSOpts: natsOpts,
	}
	a, err := agent.New(
		agentCfg, agentName, agentVersion,
		[]string{"linear-polling", "linear-api"},
		[]string{"create_issue", "update_issue", "create_comment", "add_label"},
		la.logger,
	)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	la.agent = a

	// 2. Subscribe to commands.
	_, err = a.Conn().Subscribe(protocol.SubjectCommands(agentName), la.handleCommand)
	if err != nil {
		a.Close()
		return fmt.Errorf("subscribe commands: %w", err)
	}

	// 3. Start poller.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	poller := NewPoller(la.lnClient, la.cfg.Poll.Interval, la.cfg.Poll.TeamFilter, la.publishEvent, la.logger)

	pollErrCh := make(chan error, 1)
	go func() {
		pollErrCh <- poller.Run(ctx)
	}()

	la.logger.Info().
		Str("nats", la.cfg.NATS.URL).
		Dur("poll_interval", la.cfg.Poll.Interval).
		Msg("linear agent started")

	// 4. Block on signal or stop.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		la.logger.Info().Str("signal", sig.String()).Msg("shutting down")
	case <-la.stopCh:
		la.logger.Info().Msg("stop requested, shutting down")
	case err := <-pollErrCh:
		la.logger.Error().Err(err).Msg("poller error")
		cancel()
		a.Close()
		return err
	}

	cancel()
	return la.shutdown()
}

// Stop signals the agent to shut down. Safe to call from another goroutine.
func (la *LinearAgent) Stop() {
	close(la.stopCh)
}

// NewTestAgent creates a LinearAgent configured for testing with a mock LinearClient
// and in-process NATS connection options.
func NewTestAgent(natsURL string, natsOpts []nats.Option, mockClient LinearClient, pollInterval time.Duration, logger zerolog.Logger) *LinearAgent {
	return &LinearAgent{
		cfg: Config{
			NATS: NATSConfig{URL: natsURL},
			Poll: PollConfig{Interval: pollInterval},
		},
		lnClient: mockClient,
		natsOpts: natsOpts,
		logger:   logger.With().Str("component", "linear-agent").Logger(),
		stopCh:   make(chan struct{}),
	}
}

func (la *LinearAgent) shutdown() error {
	if la.agent != nil {
		la.agent.Close()
	}
	return nil
}

func (la *LinearAgent) publishEvent(ev protocol.Event) {
	data, err := json.Marshal(ev)
	if err != nil {
		la.logger.Error().Err(err).Msg("marshal event")
		return
	}
	if err := la.agent.Conn().Publish(protocol.SubjectEvents("linear"), data); err != nil {
		la.logger.Error().Err(err).Msg("publish event")
		return
	}
	la.agent.Conn().Flush()
	la.agent.RecordEvent()
}

func (la *LinearAgent) handleCommand(msg *nats.Msg) {
	var cmd protocol.Command
	if err := json.Unmarshal(msg.Data, &cmd); err != nil {
		la.agent.RecordError()
		la.logger.Error().Err(err).Msg("unmarshal command")
		return
	}

	if !protocol.VerifyCommand(&cmd, la.cfg.Security.CommandSecret) {
		la.agent.RecordError()
		la.logger.Warn().
			Str("command", cmd.Command).
			Str("source", cmd.Source).
			Msg("rejected command: invalid or missing signature")
		return
	}

	la.logger.Info().
		Str("command", cmd.Command).
		Str("source", cmd.Source).
		Msg("received command")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var err error
	switch cmd.Command {
	case "create_issue":
		err = cmdCreateIssue(ctx, la.lnClient, cmd.Payload)
	case "update_issue":
		err = cmdUpdateIssue(ctx, la.lnClient, cmd.Payload)
	case "create_comment":
		err = cmdCreateComment(ctx, la.lnClient, cmd.Payload)
	case "add_label":
		err = cmdAddLabel(ctx, la.lnClient, cmd.Payload)
	default:
		err = fmt.Errorf("unknown command: %s", cmd.Command)
	}

	if err != nil {
		la.agent.RecordError()
		la.logger.Error().Err(err).Str("command", cmd.Command).Msg("command failed")
	} else {
		la.agent.RecordEvent()
	}
}
