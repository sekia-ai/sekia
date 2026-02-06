# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test Commands

```bash
# Build both binaries
go build ./cmd/sekiad ./cmd/sekiactl

# Run all tests
go test ./...

# Run a single test by name
go test -run TestEndToEnd ./internal/server

# Vet all packages
go vet ./...
```

No Makefile or custom scripts — standard Go toolchain only.

## Architecture

Sekia is a multi-agent event bus. Two binaries (`sekiad` daemon, `sekiactl` CLI) communicate over a Unix socket. Agents connect via an embedded NATS server.

### Dependency flow

```
cmd/sekiad          cmd/sekiactl
    │                    │
    ▼                    ▼
internal/server     cmd/sekiactl/cmd
    │                    │
    ├─► internal/natsserver   (embedded NATS + JetStream)
    ├─► internal/registry     (agent tracking via NATS subscriptions)
    ├─► internal/workflow     (Lua workflow engine — event→handler→command)
    ├─► internal/api          (HTTP-over-Unix-socket API)
    │                    │
    └────────┬───────────┘
             ▼
        pkg/protocol          (shared wire types — Event, Registration, Heartbeat, API responses)
             ▲
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

## Project status

Phases 1 (core infrastructure) and 2 (Lua workflow engine) are complete. Remaining phases: GitHub/Gmail/Slack/Linear agents, polish.
