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
3. Start HTTP API on Unix socket (`internal/api`)
4. Block on OS signal or `Stop()` channel
5. Shutdown in reverse order (API → registry → NATS)

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

## Project status

Phase 1 (core infrastructure) is complete. Remaining phases: Lua workflow engine, GitHub/Gmail/Slack/Linear agents, polish.
