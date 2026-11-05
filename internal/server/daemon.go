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
	"github.com/sekia-ai/sekia/internal/api"
	"github.com/sekia-ai/sekia/internal/natsserver"
	"github.com/sekia-ai/sekia/internal/registry"
	"github.com/sekia-ai/sekia/internal/workflow"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

// Daemon is the sekiad process.
type Daemon struct {
	cfg       Config
	logger    zerolog.Logger
	nats      *natsserver.Server
	registry  *registry.Registry
	engine    *workflow.Engine
	apiServer *api.Server
	startedAt time.Time
	stopCh    chan struct{}
	readyCh   chan struct{}
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

	// 3. Start workflow engine.
	if err := d.startWorkflowEngine(); err != nil {
		reg.Close()
		ns.Shutdown()
		return err
	}

	// 4. Subscribe to config reload for the daemon.
	ns.Conn().Subscribe(protocol.SubjectConfigReload, func(_ *nats.Msg) {
		d.reloadConfig()
	})
	ns.Conn().Subscribe(protocol.SubjectConfigReloadAgent("sekiad"), func(_ *nats.Msg) {
		d.reloadConfig()
	})

	// 5. Start API server.
	d.apiServer = api.New(d.cfg.Server.Socket, reg, d.engine, ns.Conn(), d.startedAt, d.logger)
	apiErrCh, err := d.startAPIServer()
	if err != nil {
		return err
	}

	d.logger.Info().
		Str("socket", d.cfg.Server.Socket).
		Msg("sekiad started")

	close(d.readyCh)

	// 6. Wait for signal, stop call, or server error.
	d.waitForShutdown(apiErrCh)

	return d.shutdown()
}

func (d *Daemon) startWorkflowEngine() error {
	if d.cfg.Security.CommandSecret == "" {
		d.logger.Warn().Msg("no command signing secret configured; commands will not be authenticated. Set security.command_secret or SEKIA_COMMAND_SECRET")
	}
	if d.cfg.Workflows.Dir == "" {
		return nil
	}
	eng := workflow.New(d.nats.Conn(), d.cfg.Workflows.Dir, d.cfg.Workflows.HandlerTimeout, d.cfg.Security.CommandSecret, d.logger)
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

func (d *Daemon) waitForShutdown(apiErrCh chan error) {
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
	}

	d.cfg = newCfg
	d.logger.Info().Msg("daemon configuration reloaded")
}

func (d *Daemon) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

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
