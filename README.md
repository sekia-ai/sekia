# Sekia

A multi-agent event bus for automating workflows across GitHub, Gmail, Linear, and Slack. Built on embedded NATS with JetStream.

Two binaries — `sekiad` (daemon) and `sekiactl` (CLI) — communicate over a Unix socket. Agents connect via an in-process NATS server and exchange events through typed subjects.

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│                            sekiad                            │
│                                                              │
│  ┌───────────────┐  ┌──────────┐  ┌───────────┐  ┌────────┐  │
│  │ Embedded NATS │  │ Registry │  │ Workflow  │  │  HTTP  │  │
│  │ + JetStream   │  │          │  │  Engine   │  │  API   │  │
│  └───────────────┘  └──────────┘  └───────────┘  └────────┘  │
│         ▲                           ▲    │           ▲       │
└─────────┼───────────────────────────┼────┼───────────┼───────┘
          │ NATS (in-process)         │    │           │ /tmp/sekiad.sock
    ┌─────┴─────┐              ┌──────┴──┐ │     ┌─────┴──────┐
    │  Agent A  │◄─────────────│ *.lua   │ │     │  sekiactl  │
    │  Agent B  │   commands   │ scripts │◄┘     └────────────┘
    └───────────┘              └─────────┘
          │                  events ↑
          └─────────────────────────┘
```

### Startup sequence

1. Start embedded NATS with JetStream
2. Create registry (subscribes to `sekia.registry` and `sekia.heartbeat.>`)
3. Start workflow engine, load `.lua` files, optionally start file watcher
4. Start HTTP API on Unix socket
5. Block on OS signal or stop channel
6. Shutdown in reverse order

### NATS subjects

| Subject | Purpose |
|---|---|
| `sekia.registry` | Agent registration announcements |
| `sekia.heartbeat.<name>` | Per-agent heartbeats (30s interval) |
| `sekia.events.<source>` | Event publishing |
| `sekia.commands.<name>` | Command delivery to agents |

## Getting started

### Build

```bash
go build ./cmd/sekiad ./cmd/sekiactl
```

### Run the daemon

```bash
./sekiad
```

### Query status

```bash
./sekiactl status
./sekiactl agents
./sekiactl workflows
```

## Configuration

Sekia uses TOML config files searched in `/etc/sekia`, `~/.config/sekia`, and `.`. Environment variables with the `SEKIA_` prefix are also supported.

Defaults:

| Key | Default |
|---|---|
| `server.socket` | `/tmp/sekiad.sock` |
| `server.listen` | `127.0.0.1:7600` |
| `nats.embedded` | `true` |
| `nats.data_dir` | `~/.local/share/sekia/nats` |
| `workflows.dir` | `~/.config/sekia/workflows` |
| `workflows.hot_reload` | `true` |

See [configs/sekia.toml](configs/sekia.toml) for an example.

## API

The daemon exposes an HTTP API over its Unix socket.

| Endpoint | Description |
|---|---|
| `GET /api/v1/status` | Daemon status, uptime, agent count, workflow count |
| `GET /api/v1/agents` | List registered agents with capabilities and stats |
| `GET /api/v1/workflows` | List loaded workflows with handler patterns and stats |
| `POST /api/v1/workflows/reload` | Reload all workflows from disk |

## Agent SDK

The `pkg/agent` package provides an SDK for building agents that auto-register and send heartbeats.

```go
package main

import (
	"github.com/sekia-ai/sekia/pkg/agent"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

func main() {
	a, err := agent.New(agent.Config{
		Registration: protocol.Registration{
			Name:         "my-agent",
			Version:      "0.1.0",
			Capabilities: []string{"read", "write"},
			Commands:     []string{"sync"},
		},
		NATSURLs: "nats://127.0.0.1:4222",
	})
	if err != nil {
		panic(err)
	}
	defer a.Close()

	// Use a.Conn() for custom NATS subscriptions
	// Call a.RecordEvent() / a.RecordError() to update counters
}
```

## Workflows

Workflows are Lua scripts that react to events and send commands to agents. Place `.lua` files in the workflow directory (default `~/.config/sekia/workflows/`).

```lua
-- ~/.config/sekia/workflows/github_labeler.lua

sekia.on("sekia.events.github", function(event)
    if event.type ~= "issue.opened" then return end

    local title = string.lower(event.payload.title or "")
    local label = "triage"
    if string.find(title, "bug") then label = "bug" end

    sekia.command("github-agent", "add_label", {
        issue = event.payload.number,
        label = label,
    })

    sekia.log("info", "labeled issue #" .. event.payload.number)
end)
```

### Lua API

| Function | Description |
|---|---|
| `sekia.on(pattern, handler)` | Register handler for NATS subject pattern (`*` and `>` wildcards) |
| `sekia.publish(subject, type, payload)` | Emit a new event |
| `sekia.command(agent, command, payload)` | Send command to an agent |
| `sekia.log(level, message)` | Log a message (`debug`, `info`, `warn`, `error`) |
| `sekia.name` | The workflow's name (derived from filename) |

Workflows run in a sandboxed Lua VM with only `base`, `table`, `string`, and `math` libraries available. Dangerous functions (`os`, `io`, `debug`, `dofile`, `load`) are removed.

When `hot_reload` is enabled (default), editing or adding `.lua` files automatically reloads the affected workflows.

## Testing

```bash
go test ./...
```

The end-to-end tests in `internal/server` start the full daemon, connect test agents in-process, and verify registration, heartbeats, API responses, and workflow event-to-command flow.

## Roadmap

- [x] Phase 1: Core infrastructure (NATS, registry, API, CLI, agent SDK)
- [x] Phase 2: Lua workflow engine
- [ ] Phase 3: GitHub agent
- [ ] Phase 4: Gmail, Slack, Linear agents
- [ ] Phase 5: Polish (docs, Docker, Homebrew, web UI)

## License

Apache 2.0
