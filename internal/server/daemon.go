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
	"github.com/sekia-ai/sekia/internal/natsserver"
	"github.com/sekia-ai/sekia/internal/registry"
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
	llm := d.llmOverride
	if llm == nil && d.cfg.AI.APIKey != "" {
		llm = ai.NewAnthropicClient(d.cfg.AI, d.logger)
		d.logger.Info().
			Str("provider", d.cfg.AI.Provider).
			Str("model", d.cfg.AI.Model).
			Msg("AI client configured")
	}

	// 4. Start workflow engine.
	if d.cfg.Security.CommandSecret == "" {
		d.logger.Warn().Msg("no command signing secret configured; commands will not be authenticated. Set security.command_secret or SEKIA_COMMAND_SECRET")
	}
	if d.cfg.Workflows.Dir != "" {
		eng := workflow.New(ns.Conn(), d.cfg.Workflows.Dir, llm, d.cfg.Workflows.HandlerTimeout, d.cfg.Security.CommandSecret, d.logger)
		if err := eng.Start(); err != nil {
			reg.Close()
			ns.Shutdown()
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
	}

	// 4b. Subscribe to config reload for the daemon.
	ns.Conn().Subscribe(protocol.SubjectConfigReload, func(_ *nats.Msg) {
		d.reloadConfig()
	})
	ns.Conn().Subscribe(protocol.SubjectConfigReloadAgent("sekiad"), func(_ *nats.Msg) {
		d.reloadConfig()
	})

	// 5. Start API server.
	d.apiServer = api.New(d.cfg.Server.Socket, reg, d.engine, ns.Conn(), d.startedAt, d.logger)
	apiLn, err := d.apiServer.Listen()
	if err != nil {
		if d.engine != nil {
			d.engine.Stop()
		}
		reg.Close()
		ns.Shutdown()
		return fmt.Errorf("api listen: %w", err)
	}
	apiErrCh := make(chan error, 1)
	go func() {
		apiErrCh <- d.apiServer.Serve(apiLn)
	}()

	// 6. Start web UI (if configured).
	var webErrCh chan error
	if d.cfg.Web.Listen != "" {
		if d.cfg.Web.Username == "" || d.cfg.Web.Password == "" {
			d.logger.Warn().Msg("web dashboard has no authentication; set web.username and web.password or SEKIA_WEB_USERNAME/SEKIA_WEB_PASSWORD")
		}
		d.webServer = web.New(web.Config{
			Listen:   d.cfg.Web.Listen,
			Username: d.cfg.Web.Username,
			Password: d.cfg.Web.Password,
		}, reg, d.engine, ns.Conn(), d.startedAt, d.logger)
		webErrCh = make(chan error, 1)
		go func() {
			webErrCh <- d.webServer.Start()
		}()
	}

	d.logger.Info().
		Str("socket", d.cfg.Server.Socket).
		Str("web", d.cfg.Web.Listen).
		Msg("sekiad started")

	close(d.readyCh)

	// 7. Wait for signal, stop call, or server error.
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

	return d.shutdown()
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

		if d.llmOverride == nil && newCfg.AI.APIKey != "" &&
			(newCfg.AI.APIKey != d.cfg.AI.APIKey || newCfg.AI.Model != d.cfg.AI.Model) {
			newLLM := ai.NewAnthropicClient(newCfg.AI, d.logger)
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
