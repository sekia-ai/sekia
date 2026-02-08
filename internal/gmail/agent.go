package gmail

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
	agentName    = "gmail-agent"
	agentVersion = "0.0.3"
)

// GmailAgent bridges Gmail events to the sekia event bus
// and executes email commands dispatched by workflows.
type GmailAgent struct {
	cfg      Config
	gmClient GmailClient
	agent    *agent.Agent
	logger   zerolog.Logger
	stopCh   chan struct{}

	// Overridable for testing.
	natsOpts []nats.Option
}

// NewAgent creates a GmailAgent. Call Run() to start.
func NewAgent(cfg Config, logger zerolog.Logger) *GmailAgent {
	client := newRealGmailClient(cfg)

	return &GmailAgent{
		cfg:      cfg,
		gmClient: client,
		logger:   logger.With().Str("component", "gmail-agent").Logger(),
		stopCh:   make(chan struct{}),
	}
}

// Run starts the agent: connects to NATS, subscribes to commands,
// starts the IMAP poller, and blocks until signal or Stop().
func (ga *GmailAgent) Run() error {
	// 1. Connect to NATS via the agent SDK.
	agentCfg := agent.Config{
		NATSUrl:  ga.cfg.NATS.URL,
		NATSOpts: ga.natsOpts,
	}
	a, err := agent.New(
		agentCfg, agentName, agentVersion,
		[]string{"gmail-imap", "gmail-smtp"},
		[]string{"send_email", "reply_email", "add_label", "archive"},
		ga.logger,
	)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	ga.agent = a

	// 2. Subscribe to commands.
	_, err = a.Conn().Subscribe(protocol.SubjectCommands(agentName), ga.handleCommand)
	if err != nil {
		a.Close()
		return fmt.Errorf("subscribe commands: %w", err)
	}

	// 3. Start IMAP poller.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	poller := NewPoller(ga.gmClient, ga.cfg.Poll.Interval, ga.cfg.Poll.Folder, ga.publishEvent, ga.logger)

	pollErrCh := make(chan error, 1)
	go func() {
		pollErrCh <- poller.Run(ctx)
	}()

	ga.logger.Info().
		Str("nats", ga.cfg.NATS.URL).
		Str("folder", ga.cfg.Poll.Folder).
		Dur("poll_interval", ga.cfg.Poll.Interval).
		Msg("gmail agent started")

	// 4. Block on signal or stop.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		ga.logger.Info().Str("signal", sig.String()).Msg("shutting down")
	case <-ga.stopCh:
		ga.logger.Info().Msg("stop requested, shutting down")
	case err := <-pollErrCh:
		ga.logger.Error().Err(err).Msg("poller error")
		cancel()
		a.Close()
		return err
	}

	cancel()
	return ga.shutdown()
}

// Stop signals the agent to shut down. Safe to call from another goroutine.
func (ga *GmailAgent) Stop() {
	close(ga.stopCh)
}

// NewTestAgent creates a GmailAgent configured for testing with a mock GmailClient
// and in-process NATS connection options.
func NewTestAgent(natsURL string, natsOpts []nats.Option, mockClient GmailClient, pollInterval time.Duration, logger zerolog.Logger) *GmailAgent {
	return &GmailAgent{
		cfg: Config{
			NATS: NATSConfig{URL: natsURL},
			Poll: PollConfig{Interval: pollInterval, Folder: "INBOX"},
		},
		gmClient: mockClient,
		natsOpts: natsOpts,
		logger:   logger.With().Str("component", "gmail-agent").Logger(),
		stopCh:   make(chan struct{}),
	}
}

func (ga *GmailAgent) shutdown() error {
	if ga.agent != nil {
		ga.agent.Close()
	}
	return nil
}

func (ga *GmailAgent) publishEvent(ev protocol.Event) {
	data, err := json.Marshal(ev)
	if err != nil {
		ga.logger.Error().Err(err).Msg("marshal event")
		return
	}
	if err := ga.agent.Conn().Publish(protocol.SubjectEvents("gmail"), data); err != nil {
		ga.logger.Error().Err(err).Msg("publish event")
		return
	}
	ga.agent.RecordEvent()
}

func (ga *GmailAgent) handleCommand(msg *nats.Msg) {
	var cmd struct {
		Command string         `json:"command"`
		Payload map[string]any `json:"payload"`
		Source  string         `json:"source"`
	}
	if err := json.Unmarshal(msg.Data, &cmd); err != nil {
		ga.agent.RecordError()
		ga.logger.Error().Err(err).Msg("unmarshal command")
		return
	}

	ga.logger.Info().
		Str("command", cmd.Command).
		Str("source", cmd.Source).
		Msg("received command")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var err error
	switch cmd.Command {
	case "send_email":
		err = cmdSendEmail(ctx, ga.gmClient, cmd.Payload)
	case "reply_email":
		err = cmdReplyEmail(ctx, ga.gmClient, cmd.Payload)
	case "add_label":
		err = cmdAddLabel(ctx, ga.gmClient, cmd.Payload)
	case "archive":
		err = cmdArchive(ctx, ga.gmClient, cmd.Payload)
	default:
		err = fmt.Errorf("unknown command: %s", cmd.Command)
	}

	if err != nil {
		ga.agent.RecordError()
		ga.logger.Error().Err(err).Str("command", cmd.Command).Msg("command failed")
	} else {
		ga.agent.RecordEvent()
	}
}
