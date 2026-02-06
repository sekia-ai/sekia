package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	gh "github.com/google/go-github/v68/github"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"golang.org/x/oauth2"

	"github.com/sekia-ai/sekia/pkg/agent"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

const (
	agentName    = "github-agent"
	agentVersion = "0.1.0"
)

// GitHubAgent bridges GitHub webhooks to the sekia event bus
// and executes GitHub API commands dispatched by workflows.
type GitHubAgent struct {
	cfg      Config
	ghClient GitHubClient
	agent    *agent.Agent
	webhook  *WebhookServer
	logger   zerolog.Logger
	stopCh   chan struct{}

	// Overridable for testing.
	natsOpts []nats.Option
}

// NewAgent creates a GitHubAgent. Call Run() to start.
func NewAgent(cfg Config, logger zerolog.Logger) *GitHubAgent {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: cfg.GitHub.Token})
	httpClient := oauth2.NewClient(context.Background(), ts)
	ghc := gh.NewClient(httpClient)

	return &GitHubAgent{
		cfg:      cfg,
		ghClient: &realGitHubClient{client: ghc},
		logger:   logger.With().Str("component", "github-agent").Logger(),
		stopCh:   make(chan struct{}),
	}
}

// Run starts the agent: connects to NATS, subscribes to commands,
// starts the webhook server, and blocks until signal or Stop().
func (ga *GitHubAgent) Run() error {
	// 1. Connect to NATS via the agent SDK.
	agentCfg := agent.Config{
		NATSUrl:  ga.cfg.NATS.URL,
		NATSOpts: ga.natsOpts,
	}
	a, err := agent.New(
		agentCfg, agentName, agentVersion,
		[]string{"github-webhooks", "github-api"},
		[]string{"add_label", "remove_label", "create_comment", "close_issue", "reopen_issue"},
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

	// 3. Start webhook server.
	ga.webhook = NewWebhookServer(ga.cfg.Webhook, ga.publishEvent, ga.logger)
	webhookErrCh := make(chan error, 1)
	go func() {
		if err := ga.webhook.Start(); err != nil && err != http.ErrServerClosed {
			webhookErrCh <- err
		}
	}()

	ga.logger.Info().
		Str("nats", ga.cfg.NATS.URL).
		Str("webhook", ga.cfg.Webhook.Listen).
		Msg("github agent started")

	// 4. Block on signal or stop.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		ga.logger.Info().Str("signal", sig.String()).Msg("shutting down")
	case <-ga.stopCh:
		ga.logger.Info().Msg("stop requested, shutting down")
	case err := <-webhookErrCh:
		ga.logger.Error().Err(err).Msg("webhook server error")
		a.Close()
		return err
	}

	return ga.shutdown()
}

// Stop signals the agent to shut down. Safe to call from another goroutine.
func (ga *GitHubAgent) Stop() {
	close(ga.stopCh)
}

// WebhookAddr returns the webhook server's listen address, or "" if not yet started.
func (ga *GitHubAgent) WebhookAddr() string {
	if ga.webhook == nil {
		return ""
	}
	return ga.webhook.Addr()
}

// NewTestAgent creates a GitHubAgent configured for testing with a mock GitHub API
// and in-process NATS connection options.
func NewTestAgent(natsURL string, natsOpts []nats.Option, ghBaseURL, webhookListen string, logger zerolog.Logger) *GitHubAgent {
	ghc := gh.NewClient(nil)
	ghc.BaseURL, _ = ghc.BaseURL.Parse(ghBaseURL + "/")

	return &GitHubAgent{
		cfg: Config{
			NATS:    NATSConfig{URL: natsURL},
			Webhook: WebhookConfig{Listen: webhookListen, Path: "/webhook"},
		},
		ghClient: &realGitHubClient{client: ghc},
		natsOpts: natsOpts,
		logger:   logger.With().Str("component", "github-agent").Logger(),
		stopCh:   make(chan struct{}),
	}
}

func (ga *GitHubAgent) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if ga.webhook != nil {
		ga.webhook.Shutdown(ctx)
	}
	if ga.agent != nil {
		ga.agent.Close()
	}
	return nil
}

// publishEvent sends a mapped GitHub event onto the NATS bus.
func (ga *GitHubAgent) publishEvent(ev protocol.Event) {
	data, err := json.Marshal(ev)
	if err != nil {
		ga.logger.Error().Err(err).Msg("marshal event")
		return
	}
	if err := ga.agent.Conn().Publish(protocol.SubjectEvents("github"), data); err != nil {
		ga.logger.Error().Err(err).Msg("publish event")
		return
	}
	ga.agent.RecordEvent()
}

// handleCommand processes incoming commands from workflows.
func (ga *GitHubAgent) handleCommand(msg *nats.Msg) {
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
	case "add_label":
		err = cmdAddLabel(ctx, ga.ghClient, cmd.Payload)
	case "remove_label":
		err = cmdRemoveLabel(ctx, ga.ghClient, cmd.Payload)
	case "create_comment":
		err = cmdCreateComment(ctx, ga.ghClient, cmd.Payload)
	case "close_issue":
		err = cmdCloseIssue(ctx, ga.ghClient, cmd.Payload)
	case "reopen_issue":
		err = cmdReopenIssue(ctx, ga.ghClient, cmd.Payload)
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
