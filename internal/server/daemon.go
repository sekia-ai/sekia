package server

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"github.com/sekia-ai/sekia/internal/ai"
	"github.com/sekia-ai/sekia/internal/api"
	"github.com/sekia-ai/sekia/internal/conversation"
	"github.com/sekia-ai/sekia/internal/natsserver"
	"github.com/sekia-ai/sekia/internal/registry"
	"github.com/sekia-ai/sekia/internal/sentinel"
	"github.com/sekia-ai/sekia/internal/skills"
	"github.com/sekia-ai/sekia/internal/web"
	"github.com/sekia-ai/sekia/internal/workflow"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

// Daemon is the sekiad process.
type Daemon struct {
	cfg         Config
	logger      zerolog.Logger
	nats        *natsserver.Server
	registry    *registry.Registry
	engine      *workflow.Engine
	sentinel    *sentinel.Sentinel
	skills      *skills.Manager
	apiServer   *api.Server
	webServer   *web.Server
	startedAt   time.Time
	stopCh      chan struct{}
	readyCh     chan struct{}
	llmOverride ai.LLMClient // set by tests to inject a mock
}

// NewDaemon creates a Daemon from config.
func NewDaemon(cfg Config, logger zerolog.Logger) *Daemon {
	return &Daemon{
		cfg:     cfg,
		logger:  logger,
		stopCh:  make(chan struct{}),
		readyCh: make(chan struct{}),
	}
}

// SetLLMClient overrides the AI client used by the workflow engine.
// Must be called before Run(). Intended for testing with a mock.
func (d *Daemon) SetLLMClient(c ai.LLMClient) {
	d.llmOverride = c
}

// Ready returns a channel that is closed when the daemon has finished starting.
// Wait on this before calling NATSClientURL or NATSConnectOpts.
func (d *Daemon) Ready() <-chan struct{} {
	return d.readyCh
}

// Run starts all subsystems and blocks until a signal is received or Stop is called.
func (d *Daemon) Run() error {
	d.startedAt = time.Now()

	// 1. Start embedded NATS.
	if d.cfg.NATS.Host != "" && d.cfg.NATS.Token == "" {
		d.logger.Warn().Msg("NATS is listening on TCP without authentication; set nats.token or SEKIA_NATS_TOKEN")
	}
	ns, err := natsserver.New(natsserver.Config{
		StoreDir: d.cfg.NATS.DataDir,
		Host:     d.cfg.NATS.Host,
		Port:     d.cfg.NATS.Port,
		Token:    d.cfg.NATS.Token,
	}, d.logger)
	if err != nil {
		return fmt.Errorf("start nats: %w", err)
	}
	d.nats = ns

	// 2. Start agent registry.
	reg, err := registry.New(ns.Conn(), d.logger)
	if err != nil {
		ns.Shutdown()
		return fmt.Errorf("start registry: %w", err)
	}
	d.registry = reg

	// 3. Create LLM client (if configured).
	llm := d.createLLMClient()

	// 4. Start workflow engine.
	if err := d.startWorkflowEngine(llm); err != nil {
		reg.Close()
		ns.Shutdown()
		return err
	}

	// 4a. Create conversation store and wire to engine.
	convoStore := conversation.NewStore(d.cfg.Conversation.MaxHistory, d.cfg.Conversation.TTL)
	if d.engine != nil {
		d.engine.SetConversationStore(conversation.NewWorkflowAdapter(convoStore))
	}
	go d.runConversationCleanup(convoStore)

	// 4b. Load skills (if configured).
	d.loadSkills()

	// 4c. Start sentinel (if configured).
	if d.cfg.Sentinel.Enabled && llm != nil {
		d.sentinel = sentinel.New(d.cfg.Sentinel, llm, ns.Conn(), reg, d.engine, d.logger)
		d.sentinel.Start()
	}

	// 4d. Subscribe to config reload for the daemon.
	ns.Conn().Subscribe(protocol.SubjectConfigReload, func(_ *nats.Msg) {
		d.reloadConfig()
	})
	ns.Conn().Subscribe(protocol.SubjectConfigReloadAgent("sekiad"), func(_ *nats.Msg) {
		d.reloadConfig()
	})

	// 5. Start API server.
	d.apiServer = api.New(d.cfg.Server.Socket, reg, d.engine, ns.Conn(), d.startedAt, d.logger)
	if d.skills != nil {
		d.apiServer.SetSkillsManager(d.skills)
	}
	apiErrCh, err := d.startAPIServer()
	if err != nil {
		return err
	}

	// 6. Start web UI (if configured).
	webErrCh := d.startWebServer(reg, ns)

	d.logger.Info().
		Str("socket", d.cfg.Server.Socket).
		Str("web", d.cfg.Web.Listen).
		Msg("sekiad started")

	close(d.readyCh)

	// 7. Wait for signal, stop call, or server error.
	d.waitForShutdown(apiErrCh, webErrCh)

	return d.shutdown()
}

func (d *Daemon) startWorkflowEngine(llm ai.LLMClient) error {
	if d.cfg.Security.CommandSecret == "" {
		d.logger.Warn().Msg("no command signing secret configured; commands will not be authenticated. Set security.command_secret or SEKIA_COMMAND_SECRET")
	}
	if d.cfg.Workflows.Dir == "" {
		return nil
	}
	eng := workflow.New(d.nats.Conn(), d.cfg.Workflows.Dir, llm, d.cfg.Workflows.HandlerTimeout, d.cfg.Security.CommandSecret, d.logger)
	if d.cfg.Workflows.VerifyIntegrity {
		eng.SetVerifyIntegrity(true)
	}
	if err := eng.Start(); err != nil {
		return fmt.Errorf("start workflow engine: %w", err)
	}
	if err := eng.LoadDir(); err != nil {
		d.logger.Warn().Err(err).Msg("failed to load workflows")
	}
	if d.cfg.Workflows.HotReload {
		if err := eng.StartWatcher(); err != nil {
			d.logger.Warn().Err(err).Msg("failed to start workflow watcher")
		}
	}
	d.engine = eng
	return nil
}

func (d *Daemon) startAPIServer() (chan error, error) {
	apiLn, err := d.apiServer.Listen()
	if err != nil {
		if d.engine != nil {
			d.engine.Stop()
		}
		d.registry.Close()
		d.nats.Shutdown()
		return nil, fmt.Errorf("api listen: %w", err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.apiServer.Serve(apiLn)
	}()
	return errCh, nil
}

func (d *Daemon) startWebServer(reg *registry.Registry, ns *natsserver.Server) chan error {
	if d.cfg.Web.Listen == "" {
		return nil
	}
	if d.cfg.Web.Username == "" || d.cfg.Web.Password == "" {
		d.logger.Warn().Msg("web dashboard has no authentication; set web.username and web.password or SEKIA_WEB_USERNAME/SEKIA_WEB_PASSWORD")
	}
	d.webServer = web.New(web.Config{
		Listen:   d.cfg.Web.Listen,
		Username: d.cfg.Web.Username,
		Password: d.cfg.Web.Password,
	}, reg, d.engine, ns.Conn(), d.startedAt, d.logger)
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.webServer.Start()
	}()
	return errCh
}

func (d *Daemon) waitForShutdown(apiErrCh, webErrCh chan error) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		d.logger.Info().Str("signal", sig.String()).Msg("shutting down")
	case <-d.stopCh:
		d.logger.Info().Msg("stop requested, shutting down")
	case err := <-apiErrCh:
		if err != nil {
			d.logger.Error().Err(err).Msg("API server error")
		}
	case err := <-webErrCh:
		if err != nil {
			d.logger.Error().Err(err).Msg("web server error")
		}
	}
}

// Stop signals the daemon to shut down. Safe to call from another goroutine.
func (d *Daemon) Stop() {
	close(d.stopCh)
}

// NATSClientURL returns the embedded NATS server's client URL.
func (d *Daemon) NATSClientURL() string {
	if d.nats == nil {
		return ""
	}
	return d.nats.ClientURL()
}

// NATSConnectOpts returns NATS connection options for in-process connections.
func (d *Daemon) NATSConnectOpts() []nats.Option {
	if d.nats == nil {
		return nil
	}
	opts := []nats.Option{nats.InProcessServer(d.nats.NATSServer())}
	if d.cfg.NATS.Token != "" {
		opts = append(opts, nats.Token(d.cfg.NATS.Token))
	}
	return opts
}

func (d *Daemon) loadSkills() {
	if d.cfg.Skills.Dir == "" {
		return
	}
	mgr := skills.NewManager(d.cfg.Skills.Dir, d.logger)
	if err := mgr.LoadAll(); err != nil {
		d.logger.Warn().Err(err).Msg("failed to load skills")
	}
	d.skills = mgr
	if d.engine != nil {
		d.engine.SetSkillsIndex(mgr.Index())
		d.engine.SetSkillResolver(mgr)
		// Load skill handler.lua files as workflows.
		for name, path := range mgr.HandlerPaths() {
			if err := d.engine.LoadWorkflow("skill:"+name, path); err != nil {
				d.logger.Warn().Err(err).Str("skill", name).Msg("failed to load skill handler")
			}
		}
	}
}

func (d *Daemon) createLLMClient() ai.LLMClient {
	if d.llmOverride != nil {
		return d.llmOverride
	}
	if d.cfg.AI.APIKey == "" {
		return nil
	}
	ac := ai.NewAnthropicClient(d.cfg.AI, d.logger)
	if d.cfg.AI.PersonaPath != "" {
		persona, err := ai.LoadPersona(d.cfg.AI.PersonaPath)
		if err != nil {
			d.logger.Warn().Err(err).Str("path", d.cfg.AI.PersonaPath).Msg("failed to load persona")
		} else if persona != "" {
			ac.SetPersonaPrompt(persona)
			d.logger.Info().Str("path", d.cfg.AI.PersonaPath).Msg("persona loaded")
		}
	}
	d.logger.Info().
		Str("provider", d.cfg.AI.Provider).
		Str("model", d.cfg.AI.Model).
		Msg("AI client configured")
	return ac
}

func (d *Daemon) runConversationCleanup(store *conversation.Store) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if removed := store.Cleanup(); removed > 0 {
				d.logger.Debug().Int("removed", removed).Msg("cleaned up expired conversations")
			}
		case <-d.stopCh:
			return
		}
	}
}

func (d *Daemon) reloadConfig() {
	d.logger.Info().Msg("reloading daemon configuration")

	newCfg, err := LoadConfig("")
	if err != nil {
		d.logger.Error().Err(err).Msg("failed to reload config")
		return
	}

	// Apply reloadable settings.
	if d.engine != nil {
		if newCfg.Workflows.HandlerTimeout != d.cfg.Workflows.HandlerTimeout {
			d.engine.SetHandlerTimeout(newCfg.Workflows.HandlerTimeout)
			d.logger.Info().Dur("handler_timeout", newCfg.Workflows.HandlerTimeout).Msg("updated handler timeout")
		}

		if newCfg.Workflows.VerifyIntegrity != d.cfg.Workflows.VerifyIntegrity {
			d.engine.SetVerifyIntegrity(newCfg.Workflows.VerifyIntegrity)
			d.logger.Info().Bool("verify_integrity", newCfg.Workflows.VerifyIntegrity).Msg("updated integrity verification")
		}

		if d.llmOverride == nil && newCfg.AI.APIKey != "" &&
			(newCfg.AI.APIKey != d.cfg.AI.APIKey || newCfg.AI.Model != d.cfg.AI.Model ||
				newCfg.AI.PersonaPath != d.cfg.AI.PersonaPath) {
			d.cfg = newCfg
			newLLM := d.createLLMClient()
			d.engine.SetLLMClient(newLLM)
			d.logger.Info().Str("model", newCfg.AI.Model).Msg("updated AI client")
		}
	}

	d.cfg = newCfg
	d.logger.Info().Msg("daemon configuration reloaded")
}

func (d *Daemon) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if d.webServer != nil {
		d.webServer.Shutdown(ctx)
	}
	if d.apiServer != nil {
		d.apiServer.Shutdown(ctx)
	}
	if d.sentinel != nil {
		d.sentinel.Stop()
	}
	if d.engine != nil {
		d.engine.Stop()
	}
	if d.registry != nil {
		d.registry.Close()
	}
	if d.nats != nil {
		d.nats.Shutdown()
	}
	return nil
}
