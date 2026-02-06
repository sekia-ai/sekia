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
)

// Daemon is the sekiad process.
type Daemon struct {
	cfg       Config
	logger    zerolog.Logger
	nats      *natsserver.Server
	registry  *registry.Registry
	apiServer *api.Server
	startedAt time.Time
	stopCh    chan struct{}
}

// NewDaemon creates a Daemon from config.
func NewDaemon(cfg Config, logger zerolog.Logger) *Daemon {
	return &Daemon{
		cfg:    cfg,
		logger: logger,
		stopCh: make(chan struct{}),
	}
}

// Run starts all subsystems and blocks until a signal is received or Stop is called.
func (d *Daemon) Run() error {
	d.startedAt = time.Now()

	// 1. Start embedded NATS.
	ns, err := natsserver.New(natsserver.Config{
		StoreDir: d.cfg.NATS.DataDir,
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

	// 3. Start API server.
	d.apiServer = api.New(d.cfg.Server.Socket, reg, d.startedAt, d.logger)
	apiErrCh := make(chan error, 1)
	go func() {
		apiErrCh <- d.apiServer.Start()
	}()

	d.logger.Info().
		Str("socket", d.cfg.Server.Socket).
		Msg("sekiad started")

	// 4. Wait for signal, stop call, or API error.
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
	return []nats.Option{nats.InProcessServer(d.nats.NATSServer())}
}

func (d *Daemon) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if d.apiServer != nil {
		d.apiServer.Shutdown(ctx)
	}
	if d.registry != nil {
		d.registry.Close()
	}
	if d.nats != nil {
		d.nats.Shutdown()
	}
	return nil
}
