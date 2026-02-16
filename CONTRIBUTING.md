# Contributing to sekia

Thanks for your interest in contributing to sekia! This document covers everything you need to get started.

## Prerequisites

- **Go 1.25+** (see `go.mod` for the exact version)
- **Git**
- A GitHub account

No Makefile, custom scripts, or external tools required — standard Go toolchain only.

## Getting Started

1. Fork the repository on GitHub
2. Clone your fork:
   ```bash
   git clone https://github.com/<your-username>/sekia.git
   cd sekia
   ```
3. Build all binaries:
   ```bash
   go build ./cmd/sekiad ./cmd/sekiactl ./cmd/sekia-github ./cmd/sekia-slack ./cmd/sekia-linear ./cmd/sekia-google ./cmd/sekia-mcp
   ```
4. Run the tests:
   ```bash
   go test ./...
   ```

## Development Workflow

1. Create a branch from `main` for your changes
2. Make your changes
3. Run `go vet ./...` and `go test -race ./...` before committing
4. Push to your fork and open a pull request against `main`

CI runs on every push to `main` and every pull request. It will:
- `go vet ./...`
- `go test -race -count=1 ./...`
- Build all seven binaries

Your PR must pass all CI checks before it can be merged.

## Project Structure

sekia is a multi-agent event bus. Seven binaries communicate over embedded NATS:

| Binary | Purpose | Source |
|---|---|---|
| `sekiad` | Daemon — NATS, registry, workflow engine, API | `cmd/sekiad`, `internal/server` |
| `sekiactl` | CLI — status, agents, workflows | `cmd/sekiactl` |
| `sekia-github` | GitHub agent — webhooks, polling, API commands | `cmd/sekia-github`, `internal/github` |
| `sekia-slack` | Slack agent — Socket Mode, API commands | `cmd/sekia-slack`, `internal/slack` |
| `sekia-linear` | Linear agent — GraphQL polling, API commands | `cmd/sekia-linear`, `internal/linear` |
| `sekia-google` | Google agent — Gmail + Calendar, OAuth2 | `cmd/sekia-google`, `internal/google` |
| `sekia-mcp` | MCP server — stdio transport for AI assistants | `cmd/sekia-mcp`, `internal/mcp` |

### Key directories

```
cmd/            # Binary entry points (one per binary)
internal/       # Private implementation packages
  server/       # Daemon orchestration
  natsserver/   # Embedded NATS + JetStream
  registry/     # Agent tracking
  workflow/     # Lua workflow engine
  api/          # HTTP-over-Unix-socket API
  web/          # Embedded web dashboard
  ai/           # LLM client (Anthropic Messages API)
  github/       # GitHub agent
  slack/        # Slack agent
  linear/       # Linear agent
  google/       # Google agent (Gmail + Calendar)
  mcp/          # MCP server
pkg/            # Public packages
  protocol/     # Shared wire types (Event, Registration, Heartbeat)
  agent/        # Agent SDK (auto-register, auto-heartbeat)
configs/        # Example config files and sample workflows
docs/           # Website and documentation (plain HTML + CSS)
```

## Coding Conventions

### Go style

- Standard `gofmt` formatting
- No external HTTP framework — use Go 1.22+ `http.ServeMux` method routing (e.g., `"GET /api/v1/status"`)
- Config via Viper: TOML files searched in `/etc/sekia`, `~/.config/sekia`, `.`; env vars with `SEKIA_` prefix
- Minimal dependencies — prefer `net/http` over SDKs when practical (see `internal/ai/`, `internal/linear/`)

### Interfaces for testability

Every agent defines a client interface for its external service (e.g., `GitHubClient`, `SlackClient`, `LinearClient`, `GmailClient`, `CalendarClient`, `DaemonAPI`). This allows tests to inject mocks without hitting real APIs. Follow this pattern when adding new agents or external integrations.

### Testing

- Each agent has end-to-end integration tests that start the full daemon with embedded NATS, connect the agent in-process, and verify the complete event-to-command flow through Lua workflows.
- Use `NewTestAgent()` helpers and `httptest.Server` for mocking external APIs.
- NATS runs in-process with `DontListen: true` (no TCP port). Test agents connect using `nats.InProcessServer()`.
- `Daemon.Stop()` uses a channel for testability — tests call `NATSClientURL()` and `NATSConnectOpts()` to connect agents.
- On macOS, Unix socket paths must be under 104 characters — test helpers use `/tmp` short paths.

Run a single test by name:
```bash
go test -run TestEndToEnd ./internal/server
```

Run all tests with race detection:
```bash
go test -race ./...
```

### NATS subjects

| Subject | Purpose |
|---|---|
| `sekia.registry` | Agent registration announcements |
| `sekia.heartbeat.<name>` | Per-agent heartbeats (30s interval) |
| `sekia.events.<source>` | Event publishing |
| `sekia.commands.<name>` | Command delivery to agents |

### Lua workflows

Workflows are `.lua` files in `~/.config/sekia/workflows/`. The Lua VM is sandboxed — only `base` (minus `dofile`/`loadfile`/`load`), `table`, `string`, and `math` libraries are available. No `os`, `io`, or `debug`. Example workflows live in `configs/workflows/`.

## Adding a New Agent

1. Create `internal/<name>/` with a client interface and agent implementation
2. Create `cmd/sekia-<name>/main.go` as the binary entry point
3. Add the binary to the build commands in:
   - `CLAUDE.md` (build section)
   - `README.md` (install section)
   - `.goreleaser.yml` (builds + archives)
   - `Dockerfile` (builder + runtime stage)
   - `.github/workflows/ci.yml` (build step)
4. Add a config example in `configs/sekia-<name>.toml`
5. Add a sample workflow in `configs/workflows/`
6. Write integration tests following the existing agent pattern (`NewTestAgent()` + mock server)
7. Update documentation:
   - `CLAUDE.md` (architecture, events, commands)
   - `README.md` (agent section)
   - `docs/docs/index.html` (documentation site)

## Documentation

When making significant changes, update all relevant docs:

- **`CLAUDE.md`** — Architecture, patterns, config, events, commands. This is the source of truth for AI-assisted development.
- **`README.md`** — User-facing overview, install, usage, examples.
- **`docs/index.html`** + **`docs/style.css`** — Landing page at sekia.ai.
- **`docs/docs/index.html`** + **`docs/docs/style.css`** — Full documentation at sekia.ai/docs/.

The website and docs are plain HTML + CSS with no build step, hosted on GitHub Pages.

## Commit Messages

Use conventional-style prefixes for clarity:

- `feat:` — new feature
- `fix:` — bug fix
- `docs:` — documentation only
- `test:` — adding or updating tests
- `ci:` — CI/CD changes
- `chore:` — maintenance (deps, release, etc.)

## Releases

Releases are automated via [goreleaser](https://goreleaser.com) and GitHub Actions. The release workflow builds binaries for linux/darwin on amd64/arm64, publishes to the Homebrew tap (`sekia-ai/homebrew-tap`), and generates checksums. Docker images are built from the multi-stage `Dockerfile`.

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).
