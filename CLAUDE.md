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
go build ./cmd/sekiad ./cmd/sekiactl ./cmd/sekia-github ./cmd/sekia-slack ./cmd/sekia-linear ./cmd/sekia-google ./cmd/sekia-mcp

# Run all tests
go test ./...

# Run a single test by name
go test -run TestEndToEnd ./internal/server

# Vet all packages
go vet ./...
```

No Makefile or custom scripts — standard Go toolchain only.

## Architecture

sekia is a multi-agent event bus. Seven binaries (`sekiad` daemon, `sekiactl` CLI, `sekia-github`, `sekia-slack`, `sekia-linear`, `sekia-google`, `sekia-mcp` MCP server) communicate over NATS. The daemon and CLI also use a Unix socket.

### Dependency flow

```
cmd/sekiad          cmd/sekiactl        cmd/sekia-github  cmd/sekia-slack  cmd/sekia-linear  cmd/sekia-google  cmd/sekia-mcp
    │                    │                    │                  │                 │                 │                 │
    ▼                    ▼                    ▼                  ▼                 ▼                 ▼                 ▼
internal/server     cmd/sekiactl/cmd    internal/github    internal/slack   internal/linear   internal/google   internal/mcp
    │                    │                    │                  │                 │                 │                 │
    ├─► internal/natsserver   (embedded NATS + JetStream)       │                 │                 │                 │
    ├─► internal/registry     (agent tracking)                  │                 │                 │                 │
    ├─► internal/workflow     (Lua workflow engine)              │                 │                 │                 │
    ├─► internal/api          (HTTP-over-Unix-socket API) ◄─────┼─────────────────┼─────────────────┼─────────────────┘
    ├─► internal/web          (embedded web dashboard)          │                 │                 │        (reads via Unix socket)
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

### Named instances (multi-tenancy)

All agent binaries (`sekia-github`, `sekia-slack`, `sekia-linear`, `sekia-google`) support `--name <instance>` for running multiple instances of the same agent type against different configurations.

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
- `sekia.ai(prompt [, opts])` — synchronous LLM call, returns `(result, err)`. Options: `model`, `max_tokens`, `temperature`, `system`
- `sekia.ai_json(prompt [, opts])` — like `sekia.ai` but requests JSON and returns a parsed Lua table
- `sekia.skill(name)` — returns full instructions for a named skill (from `SKILL.md` files)
- `sekia.conversation(platform, channel, thread)` — returns a conversation handle with methods: `:append(role, content)`, `:reply(prompt)`, `:history()`, `:metadata(key, [value])`
- `sekia.schedule(interval_seconds, handler)` — register a timer-driven handler that fires at the given interval (minimum 1 second)

**Key design decisions:**
- **Sandboxed**: only `base` (minus `dofile`/`loadfile`/`load`), `table`, `string`, `math` loaded. No `os`/`io`/`debug`.
- **Per-workflow goroutine**: each workflow gets its own `*lua.LState` and event channel for thread safety.
- **Self-event guard**: events from `workflow:<name>` skip handlers in the same workflow to prevent infinite loops.
- **Hot-reload**: fsnotify watches the workflow directory; file changes trigger reload with 500ms debounce.
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

**Persona** (`internal/ai/persona.go`): A markdown file (`~/.config/sekia/persona.md`) loaded at startup and prepended to every AI system prompt. Defines agent personality, communication style, values, and boundaries. Gracefully handles missing file (returns empty string).

**Prompt layering**: `[JSON mode prefix] + [persona.md] + [skills index] + [per-call system prompt]`

**Multi-turn conversations**: `CompleteRequest.Messages []ai.Message` — if non-empty, used instead of single `Prompt` for multi-turn conversation context.

**Config**: `[ai]` section in `sekia.toml` — `api_key`, `model`, `max_tokens`, `temperature`, `persona_path`. Env var: `SEKIA_AI_API_KEY`.

### Sentinel (`internal/sentinel/`)

AI-driven proactive check system. Reads a markdown checklist (`~/.config/sekia/sentinel.md`) on a configurable interval, gathers system context (agents, workflows), sends to the LLM, and publishes events based on the AI's assessment.

**Flow**: `sentinel.md checklist + system context → LLM → parse actions → publish events on sekia.events.sentinel`

**Event types**: `sentinel.action.required`, `sentinel.check.complete`

**Key design decisions:**
- **AI thinks about what matters** — unlike cron, sentinel evaluates whether something needs attention.
- **Standard event publishing** — workflows handle sentinel events like any other NATS event.
- **Optional** — disabled by default, requires both `sentinel.enabled = true` and an AI client.

**Config**: `[sentinel]` section in `sekia.toml` — `enabled`, `interval`, `checklist_path`. Env vars: `SEKIA_SENTINEL_ENABLED`, `SEKIA_SENTINEL_INTERVAL`.

### Skills system (`internal/skills/`)

Directory-based capability definitions with YAML frontmatter + natural language instructions. Skills metadata is injected into AI prompts; optional `handler.lua` files auto-load as workflows.

**Directory structure:**
```
~/.config/sekia/skills/
  pr-review/
    SKILL.md          # YAML frontmatter + instructions
    handler.lua       # optional, auto-loaded as workflow with "skill:" prefix
  triage/
    SKILL.md
```

**SKILL.md format**: YAML frontmatter (`name`, `description`, `triggers`, `version`) followed by natural language instructions.

**Key design decisions:**
- **Skills index** — compact summary of all skills injected into AI prompts so the LLM knows what capabilities are available.
- **`sekia.skill(name)`** — Lua function returns full instructions for a named skill (lazy loading).
- **Handler auto-loading** — `handler.lua` files in skill directories are automatically loaded as workflows with `skill:` prefix.
- **Hot-reload** — skills can be reloaded via API or CLI.

**Config**: `[skills]` section in `sekia.toml` — `dir`, `hot_reload`.

**API endpoints:** `GET /api/v1/skills` — list loaded skills.

**CLI:** `sekiactl skills list` — list loaded skills.

### Conversation store (`internal/conversation/`)

In-memory multi-turn conversation state keyed by (platform, channel, thread), with TTL eviction.

**Key design decisions:**
- **Thread-safe** — `sync.RWMutex` protects all state.
- **MaxHistory trimming** — oldest messages are dropped when the limit is exceeded.
- **TTL cleanup** — background goroutine runs every minute, removes conversations that have exceeded their TTL.
- **WorkflowAdapter** — bridges `conversation.Store` to `workflow.ConversationStore` interface using `ai.Message`, avoiding circular dependencies.
- **Metadata** — key/value pairs per conversation for workflow state (e.g., mood, context flags).

**Config**: `[conversation]` section in `sekia.toml` — `max_history` (default 50), `ttl` (default 1h).

### GitHub agent (`internal/github/`)

Standalone binary (`cmd/sekia-github/`) that bridges GitHub webhooks and/or REST API polling to the NATS event bus and executes GitHub API commands.

**Flow (webhook)**: `GitHub webhook → sekia-github → sekia.events.github → Lua workflow → sekia.commands.github-agent → sekia-github → GitHub API`

**Flow (polling)**: `GitHub REST API poll → sekia-github → sekia.events.github → Lua workflow → sekia.commands.github-agent → sekia-github → GitHub API`

**Event types (webhook)**: `github.issue.{opened,closed,reopened,labeled,assigned}`, `github.pr.{opened,closed,merged,review_requested}`, `github.push`, `github.comment.created`

**Event types (polling only)**: `github.issue.updated`, `github.pr.updated` — polling cannot distinguish fine-grained actions like labeled/assigned/reopened.

**Event types (label-filtered mode)**: `github.issue.matched` — emitted for each issue matching the configured labels and state. Payload includes `labels`, `state`, `owner`, `repo`, `number`, `title`, `body`, `author`, `url`, `polled`.

**Commands**: `add_label`, `remove_label`, `create_comment`, `close_issue`, `reopen_issue`, `approve_pr`, `add_to_project`

**`approve_pr`**: Submits an approving review on a pull request. Payload: `owner`, `repo`, `number`, optional `body`.

**`add_to_project`**: Adds a PR/issue to a GitHub Projects v2 board and optionally sets field values. Payload: `owner`, `repo`, `number`, `project_id` (global node ID, e.g. `PVT_...`), optional `fields` array. Each field object: `field_id` (e.g. `PVTF_...`) plus one value key: `text`, `number`, `date`, `single_select_option_id`, or `iteration_id`. Uses GitHub GraphQL API (`internal/github/projects.go`).

**Key design decisions:**
- **GitHubClient interface** for testability — commands and polling reads go through an interface that wraps `google/go-github`, easily mocked in tests.
- **GitHub GraphQL API for Projects v2** — `add_to_project` uses raw `net/http` GraphQL calls (`internal/github/projects.go`) because go-github has no ProjectV2 support. The `graphqlClient` reuses the same OAuth2 token. Three-step flow: resolve PR node ID → `addProjectV2ItemById` → `updateProjectV2ItemFieldValue` per field. All done in a single command to avoid request-reply complexity.
- **All events on `sekia.events.github`** — workflows filter by `event.type` field, not NATS subject.
- **Webhook HMAC-SHA256 verification** via `X-Hub-Signature-256` header (optional, controlled by `webhook.secret` config).
- **PAT auth** via `GITHUB_TOKEN` env var or config file.
- **Polling as alternative to webhooks** — configurable via `[poll]` section. Uses `google/go-github` REST API with `Since` parameter and `lastSyncTime` watermark. Cursor-based state machine: each tick fetches at most `per_tick` items (default 100), resuming from where it left off. When all items for all repos are consumed, `lastSyncTime` advances and a new cycle begins. Polling and webhooks can run simultaneously; polled events include `payload.polled = true`.
- **Push events are webhook-only** — the GitHub REST API has no equivalent for recent pushes.
- **Rate limit awareness** — logs a warning at startup if the estimated API call rate (3 calls/repo/cycle) exceeds 80% of GitHub's 5000/hour limit.
- **Webhook server is optional** — set `webhook.listen = ""` to disable; at least one of webhook or polling must be enabled.

**Config file**: `sekia-github.toml` (same search paths as `sekia.toml`). Env vars: `GITHUB_TOKEN`, `GITHUB_WEBHOOK_SECRET`, `SEKIA_NATS_URL`.

**Polling config**: `[poll]` section — `enabled` (bool, default false), `interval` (duration, default 30s), `per_tick` (int, default 100, max items per tick), `repos` (list of `"owner/repo"`, required when enabled), `labels` (list of strings, optional), `state` (string, default "open"). When `labels` is non-empty the poller switches to **label-filtered mode**: queries issues by label+state instead of time, only fetches issues (skips PRs/comments), emits `github.issue.matched` events, and does not advance `lastSyncTime`.

### Slack agent (`internal/slack/`)

Standalone binary (`cmd/sekia-slack/`) that connects to Slack via Socket Mode (WebSocket) and executes Slack API commands.

**Flow**: `Slack Socket Mode → sekia-slack → sekia.events.slack → Lua workflow → sekia.commands.slack-agent → sekia-slack → Slack API`

**Event types**: `slack.message.received`, `slack.reaction.added`, `slack.channel.created`, `slack.mention`, `slack.action.button_clicked`

**Commands**: `send_message`, `add_reaction`, `send_reply`, `update_message`

**Block Kit support**: `send_message` and `update_message` accept an optional `blocks` field (array of Block Kit block objects) alongside `text`. When `blocks` is present, the message is sent with `MsgOptionBlocks()` and `text` serves as the notification fallback. Blocks are passed as raw JSON through `slack.Blocks` custom unmarshaler — Lua workflows write Block Kit structures as nested tables directly.

**Interactive messages**: Button clicks and other `block_actions` interactions arrive via Socket Mode as `EventTypeInteractive`, are mapped by `MapInteractionCallback()` to `slack.action.button_clicked` events (or `slack.action.<type>` for non-button actions), and published to NATS. Event payload includes `action_id`, `value`, `block_id`, `user`, `channel`, `message_ts`, `message_text`. Requires **Interactivity** enabled in the Slack app settings (no Request URL needed with Socket Mode).

**Key design decisions:**
- **Socket Mode** — WebSocket connection to Slack, no public URL needed.
- **SlackClient interface** for testability — wraps `slack-go/slack`.
- **Bot self-message filtering** — resolves bot user ID via `AuthTest`, skips own messages.
- **Test bypass** — `NewTestAgent()` skips Socket Mode; tests publish events directly to NATS.

**Config file**: `sekia-slack.toml`. Env vars: `SLACK_BOT_TOKEN`, `SLACK_APP_TOKEN`, `SEKIA_NATS_URL`.

**Config reload**: Supports `sekiactl config reload`. Hot-reloads `security.command_secret`. Slack tokens require a restart (Socket Mode connection is long-lived).

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

**Config reload**: Supports `sekiactl config reload`. Hot-reloads `poll.interval`, `poll.team_filter`, `security.command_secret`. API key requires a restart.

### Google agent (`internal/google/`)

Standalone binary (`cmd/sekia-google/`) that bridges Gmail and Google Calendar to the NATS event bus via Google REST APIs with OAuth2 authentication.

**Flow (Gmail)**: `Gmail REST API poll → sekia-google → sekia.events.google → Lua workflow → sekia.commands.google-agent → sekia-google → Gmail API`

**Flow (Calendar)**: `Calendar REST API poll → sekia-google → sekia.events.google → Lua workflow → sekia.commands.google-agent → sekia-google → Calendar API`

**Gmail event types**: `gmail.message.received`

**Calendar event types**: `google.calendar.event.created`, `google.calendar.event.updated`, `google.calendar.event.deleted`, `google.calendar.event.upcoming`

**Gmail commands**: `send_email`, `reply_email`, `add_label`, `remove_label`, `archive`, `trash`, `untrash`, `delete`

**Calendar commands**: `create_event`, `update_event`, `delete_event`

**Key design decisions:**
- **OAuth2 authorization code flow with loopback redirect** — user runs `sekia-google auth`, browser opens to Google consent screen, token captured via localhost redirect. Persisted to disk with auto-refresh. Configurable `auth_port` for SSH port forwarding (headless/remote server auth).
- **Gmail History API** — incremental sync via `historyId` (much more efficient than IMAP polling).
- **Calendar syncToken** — incremental sync, only fetches changed events. Handles 410 Gone (expired token) with automatic reseed.
- **Upcoming event notifications** — optional polling for events starting within N minutes, with deduplication.
- **Single binary, shared token** — one OAuth2 token covers both Gmail and Calendar scopes. `PersistentTokenSource` is thread-safe for concurrent pollers.
- **GmailClient + CalendarClient interfaces** for testability — both are mockable for unit/integration tests.
- **Services are independently enableable** — `gmail.enabled` and `calendar.enabled` in config.

**Config file**: `sekia-google.toml`. Env vars: `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, `GOOGLE_TOKEN_PATH`, `SEKIA_NATS_URL`.

**Config reload**: Supports `sekiactl config reload`. Hot-reloads `gmail.poll_interval`, `gmail.query`, `gmail.max_messages`, `calendar.poll_interval`, `calendar.upcoming_mins`, `security.command_secret`. OAuth2 credentials require a restart.

### Web dashboard (`internal/web/`)

Embedded web UI served on a configurable TCP port. Uses server-side HTML templates with htmx for dynamic updates and Alpine.js for minor interactivity.

**Key design decisions:**
- **Separate TCP listener** — does NOT touch the Unix socket API. Both read from the same `*registry.Registry` and `*workflow.Engine`.
- **`go:embed`** — all static assets (htmx, Alpine.js, SSE extension, CSS) and templates are embedded in the binary. No CDN dependency.
- **htmx polling** — status/agents/workflows cards use `hx-get` + `hx-trigger="every 5s"` for partial HTML updates.
- **SSE live events** — `EventBus` subscribes to `sekia.events.>` on NATS and fans out to browser clients via Server-Sent Events. Ring buffer (50 events) for initial page load. Capped at 50 concurrent SSE connections to prevent DoS.
- **Disabled by default** — `web.listen` defaults to empty string. Set to e.g. `:8080` to enable.
- **Security headers** — middleware sets `Content-Security-Policy` (`script-src 'self' 'unsafe-eval'` for Alpine.js), `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, and `Strict-Transport-Security`.
- **CSRF protection** — double-submit cookie pattern (`sekia_csrf` cookie + `X-CSRF-Token` header). Applied to all non-safe HTTP methods (POST, PUT, DELETE, PATCH). Cookie uses `SameSite=Strict` and `Secure` when TLS is active.

**Config**: `[web]` section in `sekia.toml`. Env var: `SEKIA_WEB_LISTEN`.

**Routes**:
- `GET /web` — full dashboard page
- `GET /web/partials/status` — status card fragment
- `GET /web/partials/agents` — agents table fragment
- `GET /web/partials/workflows` — workflows table fragment
- `GET /web/events/stream` — SSE endpoint for live events
- `GET /web/static/*` — vendored JS/CSS assets

### MCP server (`internal/mcp/`)

Standalone binary (`cmd/sekia-mcp/`) that exposes sekia capabilities to AI assistants via the Model Context Protocol. Uses stdio transport — MCP clients (Claude Desktop, Claude Code, Cursor) launch it as a subprocess.

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

### Secrets encryption (`internal/secrets/`)

Config value encryption and secret resolution with three backends: age, AWS KMS, and AWS Secrets Manager. Each binary resolves secrets independently at startup via `ResolveViperConfig()`. Plaintext values continue to work — encryption is fully opt-in.

**Inline value formats:**

| Format | Backend | Behavior |
|--------|---------|----------|
| `ENC[<base64(age-ciphertext)>]` | age (`filippo.io/age`) | Decrypt with local age identity |
| `KMS[<base64(ciphertext-blob)>]` | AWS KMS | Decrypt via KMS API (key ID embedded in cipherblob) |
| `ASM[<secret-name-or-arn>]` | AWS Secrets Manager | Fetch plaintext secret by name/ARN |

**Flow**: `LoadConfig() → ResolveViperConfig(v) → detect prefixes → lazy-init backends → resolve all values → v.Set() → Unmarshal() sees plaintext`

**age identity resolution** (priority order):
1. `SEKIA_AGE_KEY` env var — raw `AGE-SECRET-KEY-1...` string
2. `SEKIA_AGE_KEY_FILE` env var — path to age identity file
3. `secrets.identity` config key — path in TOML config
4. `~/.config/sekia/age.key` — default location (if exists)
5. None found → age disabled (no error unless `ENC[...]` values exist in config)

**AWS config resolution**: Standard AWS SDK v2 default chain (env vars, profile, instance role, ECS/EC2 metadata). Optional region override via `secrets.aws_region` config key.

**Key design decisions:**
- **Three coexisting backends** — age for local/simple setups, KMS for AWS-managed encryption, ASM for direct secret references. Backends are lazily initialized only when their prefix is detected.
- **Inline values** — `ENC[...]`, `KMS[...]`, `ASM[...]` strings in TOML, not a separate secrets store.
- **Per-process decryption** — each binary resolves its own config. No daemon dependency for secrets.
- **Unified resolver** — `ResolveViperConfig(v)` replaces the old three-call pattern (`ResolveIdentity` + `DecryptViperConfig` + `HasEncryptedValues`). Walks `v.AllKeys()` once, classifies values by prefix, resolves all in-place.
- **ASM is plaintext-only** — binary secrets return an error. This is an opinionated choice for simplicity and security (no accidental binary blob injection).
- **KMS auto-rotation safe** — AWS KMS preserves all previous key material versions, so ciphertexts encrypted with older versions still decrypt.
- **Fail-fast** — any resolution failure aborts startup. Clear error if backend-specific values found but backend not configured.
- **Testable** — `KMSClient` and `SecretsManagerClient` interfaces for mock injection in tests.

**CLI:**
- `sekiactl secrets keygen [--output <path>]` — generate age keypair (default `~/.config/sekia/age.key`)
- `sekiactl secrets encrypt <value> [--recipient <pubkey>]` — encrypt with age, output `ENC[...]`
- `sekiactl secrets decrypt <ENC[...]>` — decrypt age value (for debugging)
- `sekiactl secrets kms-encrypt <value> --key-id <id>` — encrypt with AWS KMS, output `KMS[...]`
- `sekiactl secrets kms-decrypt <KMS[...]>` — decrypt KMS value (for debugging)
- `sekiactl secrets asm-get <name-or-arn>` — fetch from Secrets Manager (for debugging)

**Config**: `[secrets]` section — `identity` (path to age key file), `aws_region` (optional AWS region override), `kms_key_id` (default KMS key for encryption). Env vars: `SEKIA_AGE_KEY`, `SEKIA_AGE_KEY_FILE`, `SEKIA_KMS_KEY_ID`.

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
- `sekia-github`, `sekia-slack`, `sekia-linear`, `sekia-google` — agent formulas (each depends on `sekia`)
- `sekia-mcp` — MCP server (no service, stdio-based)

**launchd services**: Every formula except `sekia-mcp` includes a Homebrew service definition (`keep_alive true`, logs to `var/log/<formula>.log`). Users manage them with `brew services start/stop/list`.

**Archives**: goreleaser builds 7 binaries (linux+darwin, amd64+arm64), packages them into per-component tarballs, signs checksums with cosign, and generates SBOMs.

## Project status

All phases complete. Docker, goreleaser, GitHub Actions CI/CD, web dashboard, MCP server, and documentation site are in place.
