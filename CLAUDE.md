# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
# Build all binaries
go build ./cmd/sekiad ./cmd/sekiactl ./cmd/sekia-github ./cmd/sekia-slack ./cmd/sekia-linear ./cmd/sekia-gmail ./cmd/sekia-mcp

# Run all tests
go test ./...

# Run a single test by name
go test -run TestEndToEnd ./internal/server

# Vet all packages
go vet ./...
```

No Makefile or custom scripts — standard Go toolchain only.

## Architecture

Sekia is a multi-agent event bus. Seven binaries (`sekiad` daemon, `sekiactl` CLI, `sekia-github`, `sekia-slack`, `sekia-linear`, `sekia-gmail` agents, `sekia-mcp` MCP server) communicate over NATS. The daemon and CLI also use a Unix socket.

### Dependency flow

```
cmd/sekiad          cmd/sekiactl        cmd/sekia-github  cmd/sekia-slack  cmd/sekia-linear  cmd/sekia-gmail  cmd/sekia-mcp
    │                    │                    │                  │                 │                 │                │
    ▼                    ▼                    ▼                  ▼                 ▼                 ▼                ▼
internal/server     cmd/sekiactl/cmd    internal/github    internal/slack   internal/linear   internal/gmail   internal/mcp
    │                    │                    │                  │                 │                 │                │
    ├─► internal/natsserver   (embedded NATS + JetStream)       │                 │                 │                │
    ├─► internal/registry     (agent tracking)                  │                 │                 │                │
    ├─► internal/workflow     (Lua workflow engine)              │                 │                 │                │
    ├─► internal/api          (HTTP-over-Unix-socket API) ◄─────┼─────────────────┼─────────────────┼────────────────┘
    ├─► internal/web          (embedded web dashboard)          │                 │                 │       (reads via Unix socket)
    │                    │                    │                  │                 │                 │
    └────────┬───────────┘                    └──────────────────┴─────────────────┴─────────────────┘
             ▼                                                  │
        pkg/protocol  ◄─────────────────────────────────────────┘
             ▲         (shared wire types — Event, Registration, Heartbeat, API responses)
             │
        pkg/agent             (SDK for building agents — auto-register, auto-heartbeat)
```

### Key wiring: Daemon.Run() startup sequence

1. Start embedded NATS with JetStream (`internal/natsserver`)
2. Create registry, which subscribes to `sekia.registry` and `sekia.heartbeat.>` (`internal/registry`)
3. Start workflow engine, load `.lua` files, optionally start fsnotify watcher (`internal/workflow`)
4. Start HTTP API on Unix socket (`internal/api`)
5. Start web UI on TCP port if configured (`internal/web`)
6. Block on OS signal or `Stop()` channel
7. Shutdown in reverse order (web → API → workflow engine → registry → NATS)

### NATS subjects

| Subject | Purpose |
|---------|---------|
| `sekia.registry` | Agent registration announcements |
| `sekia.heartbeat.<name>` | Agent heartbeats (30s interval) |
| `sekia.events.<source>` | Event publishing |
| `sekia.commands.<name>` | Command delivery to agents |

### Important patterns

- **NATS runs in-process** with `DontListen: true` by default (no TCP port). Test agents connect using `nats.InProcessServer()`.
- **Registry merges state**: heartbeats can arrive before registration (NATS async delivery), so the registration handler must not overwrite existing heartbeat data.
- **Daemon.Stop()** uses a channel for testability instead of relying solely on OS signals. Tests also use `NATSClientURL()` and `NATSConnectOpts()` to connect agents in-process.
- **Go 1.22+ ServeMux routing** (`"GET /api/v1/status"`) — no external HTTP framework.
- **Config via Viper**: TOML files searched in `/etc/sekia`, `~/.config/sekia`, `.`; env vars with `SEKIA_` prefix; code defaults.

### Workflow engine (`internal/workflow/`)

Lua-based event→handler→command engine using [gopher-lua](https://github.com/yuin/gopher-lua). Workflows are `.lua` files in a configurable directory (default `~/.config/sekia/workflows/`).

**Lua API** available as global `sekia` table:
- `sekia.on(pattern, handler)` — register handler for NATS subject pattern (supports `*` and `>` wildcards)
- `sekia.publish(subject, event_type, payload)` — emit a new event
- `sekia.command(agent, command, payload)` — send command to an agent
- `sekia.log(level, message)` — log via zerolog
- `sekia.name` — the workflow's name
- `sekia.ai(prompt [, opts])` — synchronous LLM call, returns `(result, err)`. Options: `model`, `max_tokens`, `temperature`, `system`
- `sekia.ai_json(prompt [, opts])` — like `sekia.ai` but requests JSON and returns a parsed Lua table

**Key design decisions:**
- **Sandboxed**: only `base` (minus `dofile`/`loadfile`/`load`), `table`, `string`, `math` loaded. No `os`/`io`/`debug`.
- **Per-workflow goroutine**: each workflow gets its own `*lua.LState` and event channel for thread safety.
- **Self-event guard**: events from `workflow:<name>` skip handlers in the same workflow to prevent infinite loops.
- **Hot-reload**: fsnotify watches the workflow directory; file changes trigger reload with 500ms debounce.

**API endpoints:**
- `GET /api/v1/workflows` — list loaded workflows
- `POST /api/v1/workflows/reload` — trigger full reload

### AI integration (`internal/ai/`)

LLM client for the Anthropic Messages API, wired into the Lua workflow engine as `sekia.ai()` and `sekia.ai_json()`.

**Flow**: `Lua handler calls sekia.ai(prompt) → Go LLM client → Anthropic Messages API → response text returned to Lua`

**Key design decisions:**
- **Inline Lua function** (not a separate agent) — synchronous per-workflow, safe because each workflow has its own goroutine.
- **Raw `net/http`** — calls Anthropic Messages API directly, no SDK dependency (matches Linear agent pattern).
- **`LLMClient` interface** for testability — `Daemon.SetLLMClient()` injects a mock for integration tests.
- **Optional** — if no API key configured, `sekia.ai()` returns `nil, "AI not configured"`.
- **Error handling** — returns `(nil, error_string)` on failure (never raises Lua errors, unlike `sekia.publish`/`sekia.command`).
- **120s timeout** per LLM call.

**Config**: `[ai]` section in `sekia.toml`. Env var: `SEKIA_AI_API_KEY`.

### GitHub agent (`internal/github/`)

Standalone binary (`cmd/sekia-github/`) that bridges GitHub webhooks to the NATS event bus and executes GitHub API commands.

**Flow**: `GitHub webhook → sekia-github → sekia.events.github → Lua workflow → sekia.commands.github-agent → sekia-github → GitHub API`

**Event types**: `github.issue.{opened,closed,reopened,labeled,assigned}`, `github.pr.{opened,closed,merged,review_requested}`, `github.push`, `github.comment.created`

**Commands**: `add_label`, `remove_label`, `create_comment`, `close_issue`, `reopen_issue`

**Key design decisions:**
- **GitHubClient interface** for testability — commands go through an interface that wraps `google/go-github`, easily mocked in tests.
- **All events on `sekia.events.github`** — workflows filter by `event.type` field, not NATS subject.
- **Webhook HMAC-SHA256 verification** via `X-Hub-Signature-256` header (optional, controlled by `webhook.secret` config).
- **PAT auth** via `GITHUB_TOKEN` env var or config file.

**Config file**: `sekia-github.toml` (same search paths as `sekia.toml`). Env vars: `GITHUB_TOKEN`, `GITHUB_WEBHOOK_SECRET`, `SEKIA_NATS_URL`.

### Slack agent (`internal/slack/`)

Standalone binary (`cmd/sekia-slack/`) that connects to Slack via Socket Mode (WebSocket) and executes Slack API commands.

**Flow**: `Slack Socket Mode → sekia-slack → sekia.events.slack → Lua workflow → sekia.commands.slack-agent → sekia-slack → Slack API`

**Event types**: `slack.message.received`, `slack.reaction.added`, `slack.channel.created`, `slack.mention`

**Commands**: `send_message`, `add_reaction`, `send_reply`

**Key design decisions:**
- **Socket Mode** — WebSocket connection to Slack, no public URL needed.
- **SlackClient interface** for testability — wraps `slack-go/slack`.
- **Bot self-message filtering** — resolves bot user ID via `AuthTest`, skips own messages.
- **Test bypass** — `NewTestAgent()` skips Socket Mode; tests publish events directly to NATS.

**Config file**: `sekia-slack.toml`. Env vars: `SLACK_BOT_TOKEN`, `SLACK_APP_TOKEN`, `SEKIA_NATS_URL`.

### Linear agent (`internal/linear/`)

Standalone binary (`cmd/sekia-linear/`) that polls Linear's GraphQL API and executes Linear API commands.

**Flow**: `Linear GraphQL poll → sekia-linear → sekia.events.linear → Lua workflow → sekia.commands.linear-agent → sekia-linear → Linear API`

**Event types**: `linear.issue.created`, `linear.issue.updated`, `linear.issue.completed`, `linear.comment.created`

**Commands**: `create_issue`, `update_issue`, `create_comment`, `add_label`

**Key design decisions:**
- **GraphQL polling** — periodic API queries (configurable interval, default 30s), no webhooks needed.
- **Lightweight GraphQL client** — `net/http` + JSON, no framework dependency.
- **LinearClient interface** for testability — covers both polling reads and command mutations.
- **Created vs updated vs completed** — determined by `createdAt` vs `lastSyncTime` and state name.

**Config file**: `sekia-linear.toml`. Env vars: `LINEAR_API_KEY`, `SEKIA_NATS_URL`.

### Gmail agent (`internal/gmail/`)

Standalone binary (`cmd/sekia-gmail/`) that polls Gmail via IMAP and sends emails via SMTP.

**Flow**: `IMAP poll → sekia-gmail → sekia.events.gmail → Lua workflow → sekia.commands.gmail-agent → sekia-gmail → SMTP/IMAP`

**Event types**: `gmail.message.received`

**Commands**: `send_email`, `reply_email`, `add_label`, `archive`

**Key design decisions:**
- **IMAP polling** with UID tracking — polls for unseen messages, tracks highest UID to avoid re-processing.
- **SMTP for sending** — `net/smtp` with Gmail's SMTP server; Gmail auto-copies to Sent.
- **GmailClient interface** for testability — abstracts IMAP reads and SMTP writes.
- **App Password auth** via `GMAIL_APP_PASSWORD` env var.

**Config file**: `sekia-gmail.toml`. Env vars: `GMAIL_ADDRESS`, `GMAIL_APP_PASSWORD`, `SEKIA_NATS_URL`.

### Web dashboard (`internal/web/`)

Embedded web UI served on a configurable TCP port. Uses server-side HTML templates with htmx for dynamic updates and Alpine.js for minor interactivity.

**Key design decisions:**
- **Separate TCP listener** — does NOT touch the Unix socket API. Both read from the same `*registry.Registry` and `*workflow.Engine`.
- **`go:embed`** — all static assets (htmx, Alpine.js, SSE extension, CSS) and templates are embedded in the binary. No CDN dependency.
- **htmx polling** — status/agents/workflows cards use `hx-get` + `hx-trigger="every 5s"` for partial HTML updates.
- **SSE live events** — `EventBus` subscribes to `sekia.events.>` on NATS and fans out to browser clients via Server-Sent Events. Ring buffer (50 events) for initial page load.
- **Disabled by default** — `web.listen` defaults to empty string. Set to e.g. `:8080` to enable.

**Config**: `[web]` section in `sekia.toml`. Env var: `SEKIA_WEB_LISTEN`.

**Routes**:
- `GET /web` — full dashboard page
- `GET /web/partials/status` — status card fragment
- `GET /web/partials/agents` — agents table fragment
- `GET /web/partials/workflows` — workflows table fragment
- `GET /web/events/stream` — SSE endpoint for live events
- `GET /web/static/*` — vendored JS/CSS assets

### MCP server (`internal/mcp/`)

Standalone binary (`cmd/sekia-mcp/`) that exposes Sekia capabilities to AI assistants via the Model Context Protocol. Uses stdio transport — MCP clients (Claude Desktop, Claude Code, Cursor) launch it as a subprocess.

**Flow**: `AI assistant → MCP stdio → sekia-mcp → Unix socket API (reads) + NATS (writes)`

**MCP Tools**:
- `get_status` — daemon health, uptime, NATS status, agent/workflow counts (reads Unix socket API)
- `list_agents` — connected agents with capabilities, commands, heartbeat data (reads Unix socket API)
- `list_workflows` — loaded Lua workflows with handler patterns, event/error counts (reads Unix socket API)
- `reload_workflows` — hot-reload all .lua files from disk (posts to Unix socket API)
- `publish_event` — emit synthetic event onto NATS bus to trigger workflows (publishes to NATS)
- `send_command` — send command to a connected agent (publishes to NATS)

**Key design decisions:**
- **Standalone binary with stdio** — follows MCP convention; clients launch `sekia-mcp` as subprocess.
- **Dual communication** — reads daemon state via Unix socket API, writes events/commands via direct NATS connection.
- **No agent registration** — unlike the other agents, does not use `pkg/agent`. It's a thin protocol adapter, not an event-processing agent.
- **`DaemonAPI` interface** for testability — tests inject a mock API client.
- **Library**: `github.com/mark3labs/mcp-go` for MCP protocol handling.

**Config file**: `sekia-mcp.toml`. Env vars: `SEKIA_NATS_URL`, `SEKIA_DAEMON_SOCKET`.

## Project status

All phases complete. Docker, goreleaser, GitHub Actions CI/CD, web dashboard, and MCP server are in place.
