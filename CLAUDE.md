# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Core Instructions

- Update CLAUDE.md
- Update README.md
- Update website (docs/)
- Update documentation site (docs/docs/)
- Update SECURITY.md if relevant to security features

## Build & Test Commands

```bash
# Build all binaries
go build ./cmd/sekiad ./cmd/sekiactl ./cmd/sekia-github ./cmd/sekia-google

# Run all tests
go test ./...

# Run a single test by name
go test -run TestEndToEnd ./internal/server

# Vet all packages
go vet ./...
```

The `Makefile` only wraps the commands above (`make build`, `make test`, `make vet`, `make clean`, `make docker`); nothing beyond the standard Go toolchain is required.

## Architecture

sekia is a multi-agent event bus. Four binaries (`sekiad` daemon, `sekiactl` CLI, `sekia-github`, `sekia-google`) communicate over NATS. The daemon and CLI also use a Unix socket.

### Dependency flow

```
cmd/sekiad          cmd/sekiactl        cmd/sekia-github   cmd/sekia-google
    │                    │                    │                  │
    ▼                    ▼                    ▼                  ▼
internal/server     cmd/sekiactl/cmd    internal/github    internal/google
    │                    │                    │                  │
    ├─► internal/natsserver   (embedded NATS + JetStream)        │
    ├─► internal/registry     (agent tracking)                   │
    ├─► internal/workflow     (Lua workflow engine)              │
    ├─► internal/api          (HTTP-over-Unix-socket API)        │
    │                    │                    │                  │
    └────────┬───────────┘                    └────────┬─────────┘
             ▼                                         │
        pkg/protocol  ◄────────────────────────────────┤
             ▲         (shared wire types — Event, Registration, Heartbeat, API responses)
             │                                         │
        pkg/agent     ◄────────────────────────────────┘
                       (SDK for building agents — auto-register, auto-heartbeat)
```

### Key wiring: Daemon.Run() startup sequence

1. Start embedded NATS with JetStream (`internal/natsserver`)
2. Create registry, which subscribes to `sekia.registry` and `sekia.heartbeat.>` (`internal/registry`)
3. Start workflow engine, load `.lua` files, optionally start fsnotify watcher (`internal/workflow`)
4. Subscribe to the config-reload subjects
5. Start HTTP API on Unix socket (`internal/api`)
6. Block on OS signal or `Stop()` channel
7. Shutdown in reverse order (API → workflow engine → registry → NATS)

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
- **Config via Viper**: TOML files searched in `/etc/sekia`, `~/.config/sekia`, `.`; env vars with `SEKIA_` prefix; code defaults. `LoadConfig` records the file it read in `Config.ConfigFile`, and every reload handler re-reads that same file (plus the `--name` it started with) instead of searching again.
- **Socket path discovery** (`pkg/sockpath`): `$XDG_RUNTIME_DIR/sekia/sekiad.sock`, else `/run/user/<uid>/sekia/sekiad.sock` when that directory exists, else `~/.config/sekia/sekiad.sock`. sekiad and sekiactl share the rule, so a CLI run from an SSH session that lacks `XDG_RUNTIME_DIR` still finds a systemd-started daemon.
- **Log level**: `[log] level` (default `info`; env `SEKIA_LOG_LEVEL`; flag `--log-level`) on every binary. Debug logging prints one line per routed event and per polled item, so keep `info` in production.

### Daemon config and API

**Config**: `sekia.toml` sections — `[server]` (`socket`), `[nats]` (`embedded`, `data_dir`, `host`, `port`, `token`; env `SEKIA_NATS_TOKEN`), `[workflows]` (`dir`, `hot_reload`, `handler_timeout`, `verify_integrity`), `[security]` (`command_secret`; env `SEKIA_COMMAND_SECRET`), `[secrets]` (`aws_region`, `kms_key_id`; env `SEKIA_KMS_KEY_ID`), `[log]` (`level`; env `SEKIA_LOG_LEVEL`). Commands sent by workflows are HMAC-SHA256 signed with `security.command_secret`; agents verify them with the same secret.

**API endpoints** (`internal/api`, HTTP over the Unix socket):
- `GET /api/v1/status`
- `GET /api/v1/agents`
- `GET /api/v1/workflows`
- `POST /api/v1/workflows/reload`
- `POST /api/v1/config/reload?target=<name|*>` — ask the daemon and every agent, or a single component, to re-read its config

**CLI** (`cmd/sekiactl/cmd/`, global flag `--socket`): `status`, `agents`, `workflows [list|reload|sign]`, `config reload [--target <name>]`, `secrets kms-encrypt|kms-decrypt`, `service create|start|stop|restart|remove|list`.

### Named instances (multi-tenancy)

All agent binaries (`sekia-github`, `sekia-google`) support `--name <instance>` for running multiple instances of the same agent type against different configurations.

**Behavior when `--name` is set:**
- Config file becomes `sekia-{agent}-{name}.toml` (e.g., `sekia-github --name work` → `sekia-github-work.toml`)
- Agent registers on NATS as `{name}` instead of `{agent}-agent` (e.g., `work` instead of `github-agent`)
- Command subscription uses `sekia.commands.{name}` — workflows target named instances via `sekia.command("work", "add_label", payload)`
- `--config` still overrides the config file path regardless of `--name`

**Without `--name`:** behavior is unchanged (defaults to `github-agent`, `sekia-github.toml`, etc.).

**Example multi-instance setup:**
```bash
# Personal GitHub account
sekia-github --name github-personal   # reads sekia-github-personal.toml, registers as "github-personal"

# Work GitHub account
sekia-github --name github-work       # reads sekia-github-work.toml, registers as "github-work"
```

**Service management** (`internal/service/`):
Named instances can run as background services via `sekiactl service`. Platform-specific:
- **macOS**: launchd plists in `~/Library/LaunchAgents/com.sekia.{name}.plist`
- **Linux**: systemd user units in `~/.config/systemd/user/sekia-{name}.service`

Build tags (`service_darwin.go`, `service_linux.go`) separate platform implementations.

**CLI:**
- `sekiactl service create <binary> --name <instance> [--config <path>] [--env KEY=VALUE]` — generate service file
- `sekiactl service start <name>` — start service
- `sekiactl service stop <name>` — stop service
- `sekiactl service restart <name>` — restart service
- `sekiactl service remove <name>` — stop + delete service file
- `sekiactl service list` — list managed services (name, binary, status, PID)

Logs go to `~/.config/sekia/logs/{name}.log`. The default brew-managed instance is not affected.

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
- **Handler timeout**: each handler invocation is bounded by `workflows.handler_timeout` (default 30s) so a runaway handler cannot block the workflow goroutine.
- **Integrity verification** (optional): SHA256 manifest (`workflows.sha256`) checked before loading each `.lua` file. Manifest change via fsnotify triggers `ReloadAll()`.

**Integrity verification:**
When `workflows.verify_integrity = true`, every `LoadWorkflow()` call reads the `workflows.sha256` manifest from the workflow directory, computes the SHA256 hash of the `.lua` file, and rejects the load if the hash doesn't match or the file is not in the manifest. The manifest uses `sha256sum`-compatible format: `<64-hex>  <filename>`. Generate or update it with `sekiactl workflows sign [--dir <path>]`. During hot-reload, changes to `workflows.sha256` trigger a full `ReloadAll()`.

**Config**: `[workflows]` section in `sekia.toml` — `dir`, `hot_reload`, `handler_timeout`, `verify_integrity`.

**API endpoints:**
- `GET /api/v1/workflows` — list loaded workflows
- `POST /api/v1/workflows/reload` — trigger full reload

**CLI:**
- `sekiactl workflows` / `sekiactl workflows list` — list loaded workflows
- `sekiactl workflows reload` — reload all workflows from disk
- `sekiactl workflows sign [--dir <path>]` — generate/update SHA256 manifest

**Shipped examples** (`configs/workflows/`): `github-auto-label.lua`, `gmail-auto-reply.lua`.

### GitHub agent (`internal/github/`)

Standalone binary (`cmd/sekia-github/`) that bridges GitHub webhooks and/or REST API polling to the NATS event bus and executes GitHub API commands.

**Flow (webhook)**: `GitHub webhook → sekia-github → sekia.events.github → Lua workflow → sekia.commands.github-agent → sekia-github → GitHub API`

**Flow (polling)**: `GitHub REST API poll → sekia-github → sekia.events.github → Lua workflow → sekia.commands.github-agent → sekia-github → GitHub API`

**Event types (webhook)**: `github.issue.{opened,closed,reopened,labeled,assigned}`, `github.pr.{opened,closed,merged,review_requested}`, `github.push`, `github.comment.created`

**Event types (polling only)**: `github.issue.updated`, `github.pr.updated` — polling cannot distinguish fine-grained actions like labeled/assigned/reopened.

**Event types (label-filtered mode)**: `github.issue.matched` — emitted for each issue matching the configured labels and state. Payload includes `labels`, `state`, `owner`, `repo`, `number`, `title`, `body`, `author`, `url`, `polled`.

**Event types (PR-match mode)**: `github.pr.matched` — emitted for each PR matching the configured state and optional labels. Payload includes `owner`, `repo`, `number`, `title`, `body`, `author`, `state`, `head_branch`, `base_branch`, `url`, `draft`, `labels`, `polled`.

**Commands**: `add_label`, `remove_label`, `create_comment`, `close_issue`, `reopen_issue`, `approve_pr`, `add_to_project`

**`approve_pr`**: Submits an approving review on a pull request. Idempotent — checks for existing approval by the authenticated user before submitting. Payload: `owner`, `repo`, `number`, optional `body`.

**`add_to_project`**: Adds a PR/issue to a GitHub Projects v2 board and optionally sets field values. Payload: `owner`, `repo`, `number`, `project_id` (global node ID, e.g. `PVT_...`), optional `fields` array. Each field object: `field_id` (e.g. `PVTF_...`) plus one value key: `text`, `number`, `date`, `single_select_option_id`, or `iteration_id`. Uses GitHub GraphQL API (`internal/github/projects.go`).

**Key design decisions:**
- **GitHubClient interface** for testability — commands and polling reads go through an interface that wraps `google/go-github`, easily mocked in tests.
- **GitHub GraphQL API for Projects v2** — `add_to_project` uses raw `net/http` GraphQL calls (`internal/github/projects.go`) because go-github has no ProjectV2 support. The `graphqlClient` reuses the same OAuth2 token. Three-step flow: resolve PR node ID → `addProjectV2ItemById` → `updateProjectV2ItemFieldValue` per field. All done in a single command to avoid request-reply complexity.
- **All events on `sekia.events.github`** — workflows filter by `event.type` field, not NATS subject.
- **Webhook HMAC-SHA256 verification** via `X-Hub-Signature-256` header (optional, controlled by `webhook.secret` config).
- **PAT auth** via `GITHUB_TOKEN` env var or config file.
- **Incremental polling** (`internal/github/poller.go`) — every tick fetches only items updated since the previous successful tick, 100 per page, sorted by updated time. Time mode keeps a wall-clock watermark (`lastSync`, starts 5 minutes back); label and PR-match modes keep the newest `updated_at` seen (`watermark`) and a dedup map of `owner/repo#number → updated_at`, so an unchanged item is not re-emitted between sweeps. Nothing is persisted: a restart sweeps again. Polling and webhooks can run simultaneously; polled events include `payload.polled = true`.
- **Sweeps** — in label and PR-match modes the first tick emits every matching item, and a sweep repeats every `poll.sweep_interval` (default 1h, 0 = startup only) so an event lost while the daemon or an agent was down is replayed. Matched events carry `payload.sweep` (true during a sweep). Rules that act on matched events must be idempotent; `approve_pr` already is.
- **Errors and backoff** — a failed tick logs `poll failed`, leaves every watermark untouched and backs off: `RateLimitError` waits until GitHub's reset time, `AbuseRateLimitError` honors `Retry-After`, anything else doubles from `interval` up to 15 minutes. The GitHub HTTP client has a 30s timeout and each tick is bounded by 10 minutes, so a stalled connection cannot hang the poller.
- **Push events are webhook-only** — the GitHub REST API has no equivalent for recent pushes.
- **Rate limit awareness** — logs a warning at startup if the estimated steady-state call rate (repos × labels, or repos × 3 in time mode, per tick) exceeds 80% of GitHub's 5000/hour limit.
- **Webhook server is optional** — set `webhook.listen = ""` to disable; at least one of webhook or polling must be enabled.

**Config file**: `sekia-github.toml` (same search paths as `sekia.toml`). Env vars: `GITHUB_TOKEN`, `GITHUB_WEBHOOK_SECRET`, `SEKIA_NATS_URL`.

**Polling config**: `[poll]` section — `enabled` (bool, default false), `interval` (duration, default 30s, minimum 1s), `sweep_interval` (duration, default 1h, 0 disables the periodic sweep), `repos` (list of `"owner/repo"`, required when enabled), `labels` (list of strings, optional), `state` (string, default "open"), `match_prs` (bool, default false). `per_tick` was removed in 0.3.0 and is ignored. When `labels` is non-empty (and `match_prs` is false) the poller runs in **label-filtered mode**: one `since`-filtered query per label (OR semantics, deduplicated), issues only, `github.issue.matched` events. When `match_prs` is true it runs in **PR-match mode**: lists PRs in `state` sorted by updated time and stops at the first PR older than the watermark (the PR list API has no `since`), emitting `github.pr.matched`. When labels are set with `match_prs`, the Issues API is used (AND semantics) and results are filtered to PRs.

**Config reload**: `sekiactl config reload --target <name>` re-reads the config file the instance started with and applies everything under `[poll]` (the poller swaps settings via `Poller.Update`; a change of repos, labels, state or mode forces a fresh sweep, an interval change resets the ticker) plus `security.command_secret`. The GitHub token, webhook listener and NATS connection need a restart.

### Google agent (`internal/google/`)

Standalone binary (`cmd/sekia-google/`) that bridges Gmail to the NATS event bus via the Gmail REST API with OAuth2 authentication.

**Flow**: `Gmail REST API poll → sekia-google → sekia.events.google → Lua workflow → sekia.commands.google-agent → sekia-google → Gmail API`

**Event types**: `gmail.message.received`

**Commands**: `send_email`, `reply_email`, `add_label`, `remove_label`, `archive`, `trash`, `untrash`, `delete`

**Key design decisions:**
- **OAuth2 authorization code flow with loopback redirect** — user runs `sekia-google auth`, browser opens to Google consent screen, token captured via localhost redirect. Persisted to disk with auto-refresh. Configurable `auth_port` for SSH port forwarding (headless/remote server auth).
- **Gmail History API** — incremental sync via `historyId` (much more efficient than IMAP polling).
- **`PersistentTokenSource`** — thread-safe token source that writes refreshed tokens back to `token_path`.
- **GmailClient interface** for testability — mockable for unit/integration tests.
- **Gmail only** — `gmail.enabled` must be `true`; there is no other service to enable.

**Config file**: `sekia-google.toml` — `[nats]` (`url`, `token`), `[google]` (`client_id`, `client_secret`, `token_path`, `auth_port`), `[gmail]` (`enabled`, `poll_interval`, `user_id`, `query`, `max_messages`), `[security]` (`command_secret`), `[secrets]`. Env vars: `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_TOKEN_PATH`, `SEKIA_NATS_URL`.

**Config reload**: Supports `sekiactl config reload`. Hot-reloads `gmail.poll_interval`, `gmail.query`, `gmail.max_messages`, `security.command_secret`. OAuth2 credentials require a restart.

### Secrets encryption (`internal/secrets/`)

Config value encryption with AWS KMS. Each binary resolves secrets independently at startup via `ResolveViperConfig()`. Plaintext values continue to work — encryption is fully opt-in.

**Inline value format:**

| Format | Backend | Behavior |
|--------|---------|----------|
| `KMS[<base64(ciphertext-blob)>]` | AWS KMS | Decrypt via KMS API (key ID embedded in cipherblob) |

**Flow**: `LoadConfig() → ResolveViperConfig(v) → detect KMS[...] values → lazy-init KMS client → decrypt all values → v.Set() → Unmarshal() sees plaintext`

**AWS config resolution**: Standard AWS SDK v2 default chain (env vars, profile, instance role, ECS/EC2 metadata). Optional region override via `secrets.aws_region` config key.

**Key design decisions:**
- **Inline values** — `KMS[...]` strings in TOML, not a separate secrets store. Any other value is used as a literal string.
- **Per-process decryption** — each binary resolves its own config. No daemon dependency for secrets.
- **Unified resolver** — `ResolveViperConfig(v)` walks `v.AllKeys()` once, detects `KMS[...]` values, resolves all in-place. The KMS client is initialized only when such a value exists.
- **KMS auto-rotation safe** — AWS KMS preserves all previous key material versions, so ciphertexts encrypted with older versions still decrypt.
- **Fail-fast** — any resolution failure aborts startup.
- **Testable** — `KMSClient` interface for mock injection in tests.

**CLI:**
- `sekiactl secrets kms-encrypt <value> --key-id <id>` — encrypt with AWS KMS, output `KMS[...]`
- `sekiactl secrets kms-decrypt <KMS[...]>` — decrypt KMS value (for debugging)

**Config**: `[secrets]` section — `aws_region` (optional AWS region override), `kms_key_id` (default KMS key for `sekiactl secrets kms-encrypt`). Env var: `SEKIA_KMS_KEY_ID`.

### Website and documentation (`docs/`)

Static website at `sekia.ai` with comprehensive documentation at `sekia.ai/docs/`. Plain HTML + CSS, no build step, hosted on GitHub Pages.

**Key files:**
- `docs/index.html` — landing page
- `docs/style.css` — landing page styles
- `docs/docs/index.html` — comprehensive documentation (single page, sidebar nav)
- `docs/docs/style.css` — documentation styles

**Design decisions:**
- **Plain HTML** — no static site generator, no framework, no build step.
- **Single-page docs** — all documentation in one page for easy Cmd+F search. Sidebar navigation with scroll tracking.
- **Same design system** — shared CSS variables, fonts, colors, dark mode.

### Distribution and services (`.goreleaser.yml`)

**Homebrew**: Each component is a separate formula in the `sekia-ai/homebrew-tap` repository, installed individually:
- `sekia` — daemon (`sekiad`) + CLI (`sekiactl`)
- `sekia-github`, `sekia-google` — agent formulas (each depends on `sekia`)

**launchd services**: Every formula includes a Homebrew service definition (`keep_alive true`, logs to `var/log/<formula>.log`). Users manage them with `brew services start/stop/list`.

**Archives**: goreleaser builds 4 binaries (linux+darwin, amd64+arm64), packages them into three per-component tarballs (`sekia`, `sekia-github`, `sekia-google`), signs checksums with cosign, and generates SBOMs.

**Docker**: `Dockerfile` (`golang:1.27-alpine` builder) has targets `sekiad`, `sekia-github`, `sekia-google`; `docker-compose.yml` runs the same three services.

**CI** (`.github/workflows/ci.yml`): `go vet`, `go test -race`, `go build` of the 4 binaries, `govulncheck` + `gosec`, Trivy scan of the `sekiad` image.

## Project status

0.3.0 trims sekia to the GitHub and Google (Gmail) agents. Docker, goreleaser, GitHub Actions CI and the documentation site remain.
