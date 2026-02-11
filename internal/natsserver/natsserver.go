package natsserver

import (
	"fmt"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/rs/zerolog"
)

// Config holds settings for the embedded NATS server.
type Config struct {
	StoreDir string
	Host     string
	Port     int
	Token    string // If non-empty, requires token auth for NATS connections.
}

// Server wraps an embedded NATS server with JetStream.
type Server struct {
	ns     *server.Server
	nc     *nats.Conn
	js     jetstream.JetStream
	logger zerolog.Logger
}

// New creates and starts the embedded NATS server.
func New(cfg Config, logger zerolog.Logger) (*Server, error) {
	opts := &server.Options{
		JetStream:  true,
		StoreDir:   cfg.StoreDir,
		DontListen: cfg.Host == "",
		Host:       cfg.Host,
		Port:       cfg.Port,
		NoLog:      true,
		NoSigs:     true,
	}
	if cfg.Token != "" {
		opts.Authorization = cfg.Token
	}

	ns, err := server.NewServer(opts)
	if err != nil {
		return nil, fmt.Errorf("nats server create: %w", err)
	}

	ns.SetLoggerV2(newZerologAdapter(logger), false, false, false)

	go ns.Start()

	if !ns.ReadyForConnections(10 * time.Second) {
		return nil, fmt.Errorf("nats server failed to become ready")
	}

	var connectOpts []nats.Option
	if opts.DontListen {
		connectOpts = append(connectOpts, nats.InProcessServer(ns))
	}
	if cfg.Token != "" {
		connectOpts = append(connectOpts, nats.Token(cfg.Token))
	}
	nc, err := nats.Connect(ns.ClientURL(), connectOpts...)
	if err != nil {
		ns.Shutdown()
		return nil, fmt.Errorf("nats connect: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		ns.Shutdown()
		return nil, fmt.Errorf("jetstream init: %w", err)
	}

	logger.Info().Str("client_url", ns.ClientURL()).Msg("embedded NATS started")

	return &Server{ns: ns, nc: nc, js: js, logger: logger}, nil
}

// Conn returns the internal NATS client connection.
func (s *Server) Conn() *nats.Conn { return s.nc }

// JetStream returns the JetStream handle.
func (s *Server) JetStream() jetstream.JetStream { return s.js }

// NATSServer returns the raw server for InProcessServer connections.
func (s *Server) NATSServer() *server.Server { return s.ns }

// ClientURL returns the NATS client connection URL.
func (s *Server) ClientURL() string { return s.ns.ClientURL() }

// Shutdown gracefully drains and shuts down.
func (s *Server) Shutdown() {
	s.logger.Info().Msg("shutting down embedded NATS")
	s.nc.Drain()
	s.ns.Shutdown()
	s.ns.WaitForShutdown()
}
