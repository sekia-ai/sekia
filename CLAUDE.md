# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
# Build all binaries
go build ./cmd/sekiad ./cmd/sekiactl ./cmd/sekia-github

# Run all tests
go test ./...

# Run a single test by name
go test -run TestEndToEnd ./internal/server

# Vet all packages
go vet ./...
```

No Makefile or custom scripts — standard Go toolchain only.

## Architecture

Sekia is a multi-agent event bus. Three binaries (`sekiad` daemon, `sekiactl` CLI, `sekia-github` GitHub agent) communicate over NATS. The daemon and CLI also use a Unix socket.

### Dependency flow

```
cmd/sekiad          cmd/sekiactl        cmd/sekia-github
    │                    │                    │
    ▼                    ▼                    ▼
internal/server     cmd/sekiactl/cmd    internal/github
    │                    │                    │
    ├─► internal/natsserver   (embedded NATS + JetStream)
    ├─► internal/registry     (agent tracking via NATS subscriptions)
    ├─► internal/workflow     (Lua workflow engine — event→handler→command)
    ├─► internal/api          (HTTP-over-Unix-socket API)
    │                    │                    │
    └────────┬───────────┘                    │
             ▼                                │
        pkg/protocol  ◄───────────────────────┘
             ▲         (shared wire types — Event, Registration, Heartbeat, API responses)
             │
        pkg/agent             (SDK for building agents — auto-register, auto-heartbeat)
```

### Key wiring: Daemon.Run() startup sequence

1. Start embedded NATS with JetStream (`internal/natsserver`)
2. Create registry, which subscribes to `sekia.registry` and `sekia.heartbeat.>` (`internal/registry`)
3. Start workflow engine, load `.lua` files, optionally start fsnotify watcher (`internal/workflow`)
4. Start HTTP API on Unix socket (`internal/api`)
5. Block on OS signal or `Stop()` channel
6. Shutdown in reverse order (API → workflow engine → registry → NATS)

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

**Key design decisions:**
- **Sandboxed**: only `base` (minus `dofile`/`loadfile`/`load`), `table`, `string`, `math` loaded. No `os`/`io`/`debug`.
- **Per-workflow goroutine**: each workflow gets its own `*lua.LState` and event channel for thread safety.
- **Self-event guard**: events from `workflow:<name>` skip handlers in the same workflow to prevent infinite loops.
- **Hot-reload**: fsnotify watches the workflow directory; file changes trigger reload with 500ms debounce.

**API endpoints:**
- `GET /api/v1/workflows` — list loaded workflows
- `POST /api/v1/workflows/reload` — trigger full reload

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

## Project status

Phases 1 (core infrastructure), 2 (Lua workflow engine), and 3 (GitHub agent) are complete. Remaining phases: Gmail/Slack/Linear agents, polish.
