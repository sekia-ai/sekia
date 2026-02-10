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
	agentVersion = "0.0.4"
)

// GitHubAgent bridges GitHub webhooks and/or REST API polling to the sekia
// event bus and executes GitHub API commands dispatched by workflows.
type GitHubAgent struct {
	cfg          Config
	ghClient     GitHubClient
	agent        *agent.Agent
	webhook      *WebhookServer
	poller       *Poller
	pollerCancel context.CancelFunc
	logger       zerolog.Logger
	stopCh       chan struct{}

	// Overridable for testing.
	natsOpts []nats.Option
	readyCh  chan struct{}
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
		readyCh:  make(chan struct{}),
	}
}

// Run starts the agent: connects to NATS, subscribes to commands,
// starts the webhook server and/or poller, and blocks until signal or Stop().
func (ga *GitHubAgent) Run() error {
	// 1. Connect to NATS via the agent SDK.
	capabilities := []string{"github-api"}
	if ga.cfg.Webhook.Listen != "" {
		capabilities = append(capabilities, "github-webhooks")
	}
	if ga.cfg.Poll.Enabled {
		capabilities = append(capabilities, "github-polling")
	}

	agentCfg := agent.Config{
		NATSUrl:  ga.cfg.NATS.URL,
		NATSOpts: ga.natsOpts,
	}
	a, err := agent.New(
		agentCfg, agentName, agentVersion,
		capabilities,
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

	// 3. Start webhook server (if configured).
	var webhookErrCh chan error
	if ga.cfg.Webhook.Listen != "" {
		ga.webhook = NewWebhookServer(ga.cfg.Webhook, ga.publishEvent, ga.logger)
		if err := ga.webhook.Listen(); err != nil {
			a.Close()
			return fmt.Errorf("listen webhook: %w", err)
		}
		webhookErrCh = make(chan error, 1)
		go func() {
			if err := ga.webhook.Serve(); err != nil && err != http.ErrServerClosed {
				webhookErrCh <- err
			}
		}()
	}

	// 4. Start poller (if configured).
	pollErrCh, err := ga.startPoller()
	if err != nil {
		ga.shutdown()
		return err
	}

	ga.logger.Info().
		Str("nats", ga.cfg.NATS.URL).
		Str("webhook", ga.cfg.Webhook.Listen).
		Bool("polling", ga.cfg.Poll.Enabled).
		Msg("github agent started")

	close(ga.readyCh)

	// 5. Block on signal or stop.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		ga.logger.Info().Str("signal", sig.String()).Msg("shutting down")
	case <-ga.stopCh:
		ga.logger.Info().Msg("stop requested, shutting down")
	case err := <-webhookErrCh:
		ga.logger.Error().Err(err).Msg("webhook server error")
		ga.shutdown()
		return err
	case err := <-pollErrCh:
		ga.logger.Error().Err(err).Msg("poller error")
		ga.shutdown()
		return err
	}

	return ga.shutdown()
}

// Stop signals the agent to shut down. Safe to call from another goroutine.
func (ga *GitHubAgent) Stop() {
	close(ga.stopCh)
}

// Ready returns a channel that is closed when the agent has finished starting.
func (ga *GitHubAgent) Ready() <-chan struct{} {
	return ga.readyCh
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
		readyCh:  make(chan struct{}),
	}
}

// startPoller initialises and launches the poller goroutine if polling is
// enabled. Returns a nil channel when polling is disabled.
func (ga *GitHubAgent) startPoller() (chan error, error) {
	if !ga.cfg.Poll.Enabled {
		return nil, nil
	}

	repos, err := ParseRepos(ga.cfg.Poll.Repos)
	if err != nil {
		return nil, fmt.Errorf("parse poll repos: %w", err)
	}
	ga.poller = NewPoller(ga.ghClient, ga.cfg.Poll.Interval, ga.cfg.Poll.PerTick, repos, ga.publishEvent, ga.logger)

	var ctx context.Context
	ctx, ga.pollerCancel = context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- ga.poller.Run(ctx)
	}()

	callsPerHour := float64(len(repos)) * 3 * (3600 / ga.cfg.Poll.Interval.Seconds())
	if callsPerHour > 4000 {
		ga.logger.Warn().
			Float64("estimated_calls_per_hour", callsPerHour).
			Msg("polling rate may approach GitHub API rate limit; consider increasing interval or reducing repos")
	}

	return errCh, nil
}

// NewTestAgentWithPolling creates a GitHubAgent configured for testing with polling
// enabled and an injected GitHubClient (for mock poll responses).
func NewTestAgentWithPolling(natsURL string, natsOpts []nats.Option, ghClient GitHubClient, pollInterval time.Duration, repos []string, webhookListen string, logger zerolog.Logger) *GitHubAgent {
	return &GitHubAgent{
		cfg: Config{
			NATS:    NATSConfig{URL: natsURL},
			Webhook: WebhookConfig{Listen: webhookListen, Path: "/webhook"},
			Poll:    PollConfig{Enabled: true, Interval: pollInterval, Repos: repos, PerTick: 100},
		},
		ghClient: ghClient,
		natsOpts: natsOpts,
		logger:   logger.With().Str("component", "github-agent").Logger(),
		stopCh:   make(chan struct{}),
		readyCh:  make(chan struct{}),
	}
}

func (ga *GitHubAgent) shutdown() error {
	if ga.pollerCancel != nil {
		ga.pollerCancel()
	}

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
