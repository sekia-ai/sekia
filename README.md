# Sekia

A multi-agent event bus for automating workflows across GitHub, Gmail, Linear, and Slack. Built on embedded NATS with JetStream.

Two binaries — `sekiad` (daemon) and `sekiactl` (CLI) — communicate over a Unix socket. Agents connect via an in-process NATS server and exchange events through typed subjects.

## Architecture

```
┌─────────────────────────────────────────────────┐
│                    sekiad                        │
│                                                  │
│  ┌──────────────┐  ┌──────────┐  ┌───────────┐  │
│  │ Embedded NATS │  │ Registry │  │  HTTP API  │  │
│  │ + JetStream   │  │          │  │ (Unix sock)│  │
│  └──────────────┘  └──────────┘  └───────────┘  │
│         ▲                              ▲         │
└─────────┼──────────────────────────────┼─────────┘
          │ NATS (in-process)            │ /tmp/sekiad.sock
    ┌─────┴─────┐                  ┌─────┴─────┐
    │  Agent A  │                  │  sekiactl  │
    │  Agent B  │                  └───────────┘
    └───────────┘
```

### Startup sequence

1. Start embedded NATS with JetStream
2. Create registry (subscribes to `sekia.registry` and `sekia.heartbeat.>`)
3. Start HTTP API on Unix socket
4. Block on OS signal or stop channel
5. Shutdown in reverse order

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

See [configs/sekia.toml](configs/sekia.toml) for an example.

## API

The daemon exposes an HTTP API over its Unix socket.

| Endpoint | Description |
|---|---|
| `GET /api/v1/status` | Daemon status, uptime, agent count |
| `GET /api/v1/agents` | List registered agents with capabilities and stats |

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

## Testing

```bash
go test ./...
```

The end-to-end test in `internal/server` starts the full daemon, connects a test agent in-process, and verifies registration, heartbeats, and API responses.

## Roadmap

- [x] Phase 1: Core infrastructure (NATS, registry, API, CLI, agent SDK)
- [ ] Phase 2: Lua workflow engine
- [ ] Phase 3: GitHub agent
- [ ] Phase 4: Gmail, Slack, Linear agents
- [ ] Phase 5: Polish (docs, Docker, Homebrew, web UI)

## License

Apache 2.0
