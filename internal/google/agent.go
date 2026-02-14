package google

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
	"golang.org/x/oauth2"

	"github.com/sekia-ai/sekia/pkg/agent"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

const (
	agentName    = "google-agent"
	agentVersion = "0.0.12"
)

// GoogleAgent bridges Google services (Gmail, Calendar) to the sekia event bus
// and executes commands dispatched by workflows.
type GoogleAgent struct {
	cfg            Config
	gmailClient    GmailClient
	calendarClient CalendarClient
	agent          *agent.Agent
	logger         zerolog.Logger
	stopCh         chan struct{}
	readyCh        chan struct{}

	// Overridable for testing.
	natsOpts []nats.Option
}

// NewAgent creates a GoogleAgent with real API clients. Call Run() to start.
func NewAgent(cfg Config, logger zerolog.Logger) (*GoogleAgent, error) {
	oauthCfg := OAuthConfig(cfg.Google.ClientID, cfg.Google.ClientSecret)
	tokenSource, err := NewPersistentTokenSource(oauthCfg, cfg.Google.TokenPath)
	if err != nil {
		return nil, fmt.Errorf("create token source: %w", err)
	}

	httpClient := oauth2.NewClient(context.Background(), tokenSource)

	ga := &GoogleAgent{
		cfg:     cfg,
		logger:  logger.With().Str("component", "google-agent").Logger(),
		stopCh:  make(chan struct{}),
		readyCh: make(chan struct{}),
	}

	if cfg.Gmail.Enabled {
		gmailClient, err := newRealGmailClient(httpClient)
		if err != nil {
			return nil, fmt.Errorf("create gmail client: %w", err)
		}
		ga.gmailClient = gmailClient
	}

	if cfg.Calendar.Enabled {
		calendarClient, err := newRealCalendarClient(httpClient)
		if err != nil {
			return nil, fmt.Errorf("create calendar client: %w", err)
		}
		ga.calendarClient = calendarClient
	}

	return ga, nil
}

// NewTestAgent creates a GoogleAgent configured for testing with mock clients
// and in-process NATS connection options.
func NewTestAgent(
	natsURL string,
	natsOpts []nats.Option,
	gmailClient GmailClient,
	calendarClient CalendarClient,
	gmailPollInterval time.Duration,
	calendarPollInterval time.Duration,
	logger zerolog.Logger,
) *GoogleAgent {
	return &GoogleAgent{
		cfg: Config{
			NATS: NATSConfig{URL: natsURL},
			Gmail: GmailConfig{
				Enabled:      gmailClient != nil,
				PollInterval: gmailPollInterval,
				UserID:       "me",
			},
			Calendar: CalendarConfig{
				Enabled:      calendarClient != nil,
				PollInterval: calendarPollInterval,
				CalendarID:   "primary",
			},
		},
		gmailClient:    gmailClient,
		calendarClient: calendarClient,
		natsOpts:       natsOpts,
		logger:         logger.With().Str("component", "google-agent").Logger(),
		stopCh:         make(chan struct{}),
		readyCh:        make(chan struct{}),
	}
}

// Run starts the agent: connects to NATS, subscribes to commands,
// starts pollers, and blocks until signal or Stop().
func (ga *GoogleAgent) Run() error {
	// 1. Build capabilities and commands lists.
	var capabilities []string
	var commands []string

	if ga.cfg.Gmail.Enabled {
		capabilities = append(capabilities, "gmail-api")
		commands = append(commands, "send_email", "reply_email", "add_label", "remove_label", "archive", "trash", "delete")
	}
	if ga.cfg.Calendar.Enabled {
		capabilities = append(capabilities, "calendar-api")
		commands = append(commands, "create_event", "update_event", "delete_event")
	}

	// 2. Connect to NATS via the agent SDK.
	natsOpts := ga.natsOpts
	if ga.cfg.NATS.Token != "" {
		natsOpts = append(natsOpts, nats.Token(ga.cfg.NATS.Token))
	}
	agentCfg := agent.Config{
		NATSUrl:  ga.cfg.NATS.URL,
		NATSOpts: natsOpts,
	}
	a, err := agent.New(
		agentCfg, agentName, agentVersion,
		capabilities, commands,
		ga.logger,
	)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	ga.agent = a

	// 3. Subscribe to commands.
	_, err = a.Conn().Subscribe(protocol.SubjectCommands(agentName), ga.handleCommand)
	if err != nil {
		a.Close()
		return fmt.Errorf("subscribe commands: %w", err)
	}

	// 4. Start pollers.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 2)

	if ga.cfg.Gmail.Enabled && ga.gmailClient != nil {
		poller := NewGmailPoller(
			ga.gmailClient,
			ga.cfg.Gmail.PollInterval,
			ga.cfg.Gmail.UserID,
			ga.cfg.Gmail.Query,
			ga.cfg.Gmail.MaxMessages,
			ga.publishEvent,
			ga.logger,
		)
		go func() { errCh <- poller.Run(ctx) }()
	}

	if ga.cfg.Calendar.Enabled && ga.calendarClient != nil {
		poller := NewCalendarPoller(
			ga.calendarClient,
			ga.cfg.Calendar.PollInterval,
			ga.cfg.Calendar.CalendarID,
			ga.cfg.Calendar.UpcomingMins,
			ga.publishEvent,
			ga.logger,
		)
		go func() { errCh <- poller.Run(ctx) }()
	}

	ga.logger.Info().
		Strs("capabilities", capabilities).
		Strs("commands", commands).
		Str("nats", ga.cfg.NATS.URL).
		Msg("google agent started")

	close(ga.readyCh)

	// 5. Block on signal or stop.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		ga.logger.Info().Str("signal", sig.String()).Msg("shutting down")
	case <-ga.stopCh:
		ga.logger.Info().Msg("stop requested, shutting down")
	case err := <-errCh:
		ga.logger.Error().Err(err).Msg("poller error")
		cancel()
		a.Close()
		return err
	}

	cancel()
	return ga.shutdown()
}

// Stop signals the agent to shut down. Safe to call from another goroutine.
func (ga *GoogleAgent) Stop() {
	close(ga.stopCh)
}

// Ready returns a channel that is closed when the agent is ready.
func (ga *GoogleAgent) Ready() <-chan struct{} {
	return ga.readyCh
}

func (ga *GoogleAgent) shutdown() error {
	if ga.agent != nil {
		ga.agent.Close()
	}
	return nil
}

func (ga *GoogleAgent) publishEvent(ev protocol.Event) {
	data, err := json.Marshal(ev)
	if err != nil {
		ga.logger.Error().Err(err).Msg("marshal event")
		return
	}
	if err := ga.agent.Conn().Publish(protocol.SubjectEvents("google"), data); err != nil {
		ga.logger.Error().Err(err).Msg("publish event")
		return
	}
	ga.agent.RecordEvent()
}

func (ga *GoogleAgent) handleCommand(msg *nats.Msg) {
	var cmd protocol.Command
	if err := json.Unmarshal(msg.Data, &cmd); err != nil {
		ga.agent.RecordError()
		ga.logger.Error().Err(err).Msg("unmarshal command")
		return
	}

	if !protocol.VerifyCommand(&cmd, ga.cfg.Security.CommandSecret) {
		ga.agent.RecordError()
		ga.logger.Warn().
			Str("command", cmd.Command).
			Str("source", cmd.Source).
			Msg("rejected command: invalid or missing signature")
		return
	}

	ga.logger.Info().
		Str("command", cmd.Command).
		Str("source", cmd.Source).
		Msg("received command")

	ctx, ctxCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer ctxCancel()

	var err error
	switch cmd.Command {
	// Gmail commands
	case "send_email":
		err = cmdGmailSendEmail(ctx, ga.gmailClient, ga.cfg.Gmail.UserID, cmd.Payload)
	case "reply_email":
		err = cmdGmailReplyEmail(ctx, ga.gmailClient, ga.cfg.Gmail.UserID, cmd.Payload)
	case "add_label":
		err = cmdGmailAddLabel(ctx, ga.gmailClient, ga.cfg.Gmail.UserID, cmd.Payload)
	case "remove_label":
		err = cmdGmailRemoveLabel(ctx, ga.gmailClient, ga.cfg.Gmail.UserID, cmd.Payload)
	case "archive":
		err = cmdGmailArchive(ctx, ga.gmailClient, ga.cfg.Gmail.UserID, cmd.Payload)
	case "trash":
		err = cmdGmailTrash(ctx, ga.gmailClient, ga.cfg.Gmail.UserID, cmd.Payload)
	case "delete":
		err = cmdGmailDelete(ctx, ga.gmailClient, ga.cfg.Gmail.UserID, cmd.Payload)

	// Calendar commands
	case "create_event":
		err = cmdCalendarCreateEvent(ctx, ga.calendarClient, ga.cfg.Calendar.CalendarID, cmd.Payload)
	case "update_event":
		err = cmdCalendarUpdateEvent(ctx, ga.calendarClient, ga.cfg.Calendar.CalendarID, cmd.Payload)
	case "delete_event":
		err = cmdCalendarDeleteEvent(ctx, ga.calendarClient, ga.cfg.Calendar.CalendarID, cmd.Payload)

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
