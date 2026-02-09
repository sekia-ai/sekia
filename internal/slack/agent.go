package slack

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
	slackapi "github.com/slack-go/slack"

	"github.com/sekia-ai/sekia/pkg/agent"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

const (
	agentName    = "slack-agent"
	agentVersion = "0.0.4"
)

// SlackAgent bridges Slack events to the sekia event bus
// and executes Slack API commands dispatched by workflows.
type SlackAgent struct {
	cfg      Config
	slClient SlackClient
	agent    *agent.Agent
	logger   zerolog.Logger
	stopCh   chan struct{}

	// Overridable for testing.
	natsOpts []nats.Option
}

// NewAgent creates a SlackAgent. Call Run() to start.
func NewAgent(cfg Config, logger zerolog.Logger) *SlackAgent {
	api := slackapi.New(cfg.Slack.BotToken)

	return &SlackAgent{
		cfg:      cfg,
		slClient: &realSlackClient{client: api},
		logger:   logger.With().Str("component", "slack-agent").Logger(),
		stopCh:   make(chan struct{}),
	}
}

// Run starts the agent: connects to NATS, subscribes to commands,
// starts the Socket Mode listener, and blocks until signal or Stop().
func (sa *SlackAgent) Run() error {
	// 1. Connect to NATS via the agent SDK.
	agentCfg := agent.Config{
		NATSUrl:  sa.cfg.NATS.URL,
		NATSOpts: sa.natsOpts,
	}
	a, err := agent.New(
		agentCfg, agentName, agentVersion,
		[]string{"slack-socketmode", "slack-api"},
		[]string{"send_message", "add_reaction", "send_reply"},
		sa.logger,
	)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	sa.agent = a

	// 2. Subscribe to commands.
	_, err = a.Conn().Subscribe(protocol.SubjectCommands(agentName), sa.handleCommand)
	if err != nil {
		a.Close()
		return fmt.Errorf("subscribe commands: %w", err)
	}

	// 3. Start Socket Mode listener.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	smErrCh := make(chan error, 1)
	if sa.cfg.Slack.AppToken != "" {
		smListener := NewSocketModeListener(
			sa.cfg.Slack.BotToken,
			sa.cfg.Slack.AppToken,
			sa.publishEvent,
			sa.logger,
		)
		go func() {
			smErrCh <- smListener.Run(ctx)
		}()
	}

	sa.logger.Info().
		Str("nats", sa.cfg.NATS.URL).
		Msg("slack agent started")

	// 4. Block on signal or stop.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		sa.logger.Info().Str("signal", sig.String()).Msg("shutting down")
	case <-sa.stopCh:
		sa.logger.Info().Msg("stop requested, shutting down")
	case err := <-smErrCh:
		sa.logger.Error().Err(err).Msg("socket mode error")
		cancel()
		a.Close()
		return err
	}

	cancel()
	return sa.shutdown()
}

// Stop signals the agent to shut down. Safe to call from another goroutine.
func (sa *SlackAgent) Stop() {
	close(sa.stopCh)
}

// NewTestAgent creates a SlackAgent configured for testing with a mock Slack API
// and in-process NATS connection options. AppToken is left empty to skip Socket Mode.
func NewTestAgent(natsURL string, natsOpts []nats.Option, mockClient SlackClient, logger zerolog.Logger) *SlackAgent {
	return &SlackAgent{
		cfg: Config{
			NATS: NATSConfig{URL: natsURL},
		},
		slClient: mockClient,
		natsOpts: natsOpts,
		logger:   logger.With().Str("component", "slack-agent").Logger(),
		stopCh:   make(chan struct{}),
	}
}

func (sa *SlackAgent) shutdown() error {
	if sa.agent != nil {
		sa.agent.Close()
	}
	return nil
}

func (sa *SlackAgent) publishEvent(ev protocol.Event) {
	data, err := json.Marshal(ev)
	if err != nil {
		sa.logger.Error().Err(err).Msg("marshal event")
		return
	}
	if err := sa.agent.Conn().Publish(protocol.SubjectEvents("slack"), data); err != nil {
		sa.logger.Error().Err(err).Msg("publish event")
		return
	}
	sa.agent.RecordEvent()
}

func (sa *SlackAgent) handleCommand(msg *nats.Msg) {
	var cmd struct {
		Command string         `json:"command"`
		Payload map[string]any `json:"payload"`
		Source  string         `json:"source"`
	}
	if err := json.Unmarshal(msg.Data, &cmd); err != nil {
		sa.agent.RecordError()
		sa.logger.Error().Err(err).Msg("unmarshal command")
		return
	}

	sa.logger.Info().
		Str("command", cmd.Command).
		Str("source", cmd.Source).
		Msg("received command")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var err error
	switch cmd.Command {
	case "send_message":
		err = cmdSendMessage(ctx, sa.slClient, cmd.Payload)
	case "add_reaction":
		err = cmdAddReaction(ctx, sa.slClient, cmd.Payload)
	case "send_reply":
		err = cmdSendReply(ctx, sa.slClient, cmd.Payload)
	default:
		err = fmt.Errorf("unknown command: %s", cmd.Command)
	}

	if err != nil {
		sa.agent.RecordError()
		sa.logger.Error().Err(err).Str("command", cmd.Command).Msg("command failed")
	} else {
		sa.agent.RecordEvent()
	}
}
