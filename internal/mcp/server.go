package mcp

import (
	"context"
	"log"
	"os"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
)

// MCPServer exposes sekia capabilities to AI assistants via MCP.
type MCPServer struct {
	api           DaemonAPI
	nc            *nats.Conn
	logger        zerolog.Logger
	commandSecret string

	// Overridable for testing.
	natsOpts []nats.Option
}

// New creates an MCPServer. Call Run() to start serving on stdio.
func New(cfg Config, logger zerolog.Logger) *MCPServer {
	s := &MCPServer{
		api:           NewAPIClient(cfg.Daemon.Socket),
		logger:        logger.With().Str("component", "mcp").Logger(),
		commandSecret: cfg.Security.CommandSecret,
	}
	if cfg.NATS.Token != "" {
		s.natsOpts = append(s.natsOpts, nats.Token(cfg.NATS.Token))
	}
	return s
}

// SetDaemonAPI overrides the daemon API client. Intended for testing with a mock.
func (s *MCPServer) SetDaemonAPI(api DaemonAPI) {
	s.api = api
}

// SetNATSOpts sets NATS connection options. Must be called before Run().
func (s *MCPServer) SetNATSOpts(opts []nats.Option) {
	s.natsOpts = opts
}

// Run connects to NATS, registers MCP tools, and serves on stdio.
// It blocks until stdin is closed or the context is cancelled.
func (s *MCPServer) Run(ctx context.Context, natsURL string) error {
	// Connect to NATS for mutation tools.
	nc, err := nats.Connect(natsURL, s.natsOpts...)
	if err != nil {
		return err
	}
	defer nc.Close()
	s.nc = nc

	srv := mcpserver.NewMCPServer(
		"sekia",
		"0.0.22",
		mcpserver.WithRecovery(),
	)

	s.registerTools(srv)

	stdio := mcpserver.NewStdioServer(srv)
	stdio.SetErrorLogger(log.New(os.Stderr, "", log.LstdFlags))

	s.logger.Info().Msg("MCP server starting on stdio")
	return stdio.Listen(ctx, os.Stdin, os.Stdout)
}

func (s *MCPServer) registerTools(srv *mcpserver.MCPServer) {
	srv.AddTool(
		mcplib.NewTool("get_status",
			mcplib.WithDescription("Get sekia daemon status including uptime, NATS health, and agent/workflow counts"),
			mcplib.WithReadOnlyHintAnnotation(true),
		),
		s.handleGetStatus,
	)

	srv.AddTool(
		mcplib.NewTool("list_agents",
			mcplib.WithDescription("List all connected sekia agents with their capabilities, commands, heartbeat data, and error counts"),
			mcplib.WithReadOnlyHintAnnotation(true),
		),
		s.handleListAgents,
	)

	srv.AddTool(
		mcplib.NewTool("list_workflows",
			mcplib.WithDescription("List all loaded Lua workflows with their handler patterns, event counts, and error counts"),
			mcplib.WithReadOnlyHintAnnotation(true),
		),
		s.handleListWorkflows,
	)

	srv.AddTool(
		mcplib.NewTool("reload_workflows",
			mcplib.WithDescription("Hot-reload all Lua workflow files from disk"),
		),
		s.handleReloadWorkflows,
	)

	srv.AddTool(
		mcplib.NewTool("publish_event",
			mcplib.WithDescription("Publish a synthetic event onto the sekia NATS event bus, triggering matching Lua workflows"),
			mcplib.WithString("source", mcplib.Required(), mcplib.Description("Event source identifier (e.g. \"mcp\", \"manual-test\"). Published to sekia.events.<source>")),
			mcplib.WithString("event_type", mcplib.Required(), mcplib.Description("Event type (e.g. \"test.ping\", \"github.issue.opened\")")),
			mcplib.WithObject("payload", mcplib.Description("Arbitrary JSON payload for the event")),
		),
		s.handlePublishEvent,
	)

	srv.AddTool(
		mcplib.NewTool("send_command",
			mcplib.WithDescription("Send a command to a connected sekia agent (e.g. send Slack message, create GitHub comment, create Linear issue)"),
			mcplib.WithString("agent", mcplib.Required(), mcplib.Description("Target agent name (e.g. \"github-agent\", \"slack-agent\", \"linear-agent\", \"google-agent\")")),
			mcplib.WithString("command", mcplib.Required(), mcplib.Description("Command name (e.g. \"create_comment\", \"send_message\", \"create_issue\", \"send_email\")")),
			mcplib.WithObject("payload", mcplib.Required(), mcplib.Description("Command-specific payload")),
		),
		s.handleSendCommand,
	)
}
