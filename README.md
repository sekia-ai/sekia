<img width=128 src="logo.png" />

[![CI](https://github.com/sekia-ai/sekia/actions/workflows/ci.yml/badge.svg)](https://github.com/sekia-ai/sekia/actions/workflows/ci.yml) [![Dependabot Updates](https://github.com/sekia-ai/sekia/actions/workflows/dependabot/dependabot-updates/badge.svg)](https://github.com/sekia-ai/sekia/actions/workflows/dependabot/dependabot-updates) [![pages-build-deployment](https://github.com/sekia-ai/sekia/actions/workflows/pages/pages-build-deployment/badge.svg)](https://github.com/sekia-ai/sekia/actions/workflows/pages/pages-build-deployment) [![Release](https://github.com/sekia-ai/sekia/actions/workflows/release.yml/badge.svg)](https://github.com/sekia-ai/sekia/actions/workflows/release.yml)

# sekia

A multi-agent event bus for automating workflows across GitHub, Gmail, Linear, and Slack. Built on embedded NATS with JetStream.

Seven binaries — `sekiad` (daemon), `sekiactl` (CLI), four agents (`sekia-github`, `sekia-slack`, `sekia-linear`, `sekia-gmail`), and `sekia-mcp` (MCP server) — communicate over NATS. The daemon and CLI also use a Unix socket.

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

## Install

### Homebrew (macOS/Linux)

```bash
brew install sekia-ai/tap/sekia
```

### From source

```bash
go install github.com/sekia-ai/sekia/cmd/sekiad@latest
go install github.com/sekia-ai/sekia/cmd/sekiactl@latest
go install github.com/sekia-ai/sekia/cmd/sekia-github@latest
go install github.com/sekia-ai/sekia/cmd/sekia-slack@latest
go install github.com/sekia-ai/sekia/cmd/sekia-linear@latest
go install github.com/sekia-ai/sekia/cmd/sekia-gmail@latest
go install github.com/sekia-ai/sekia/cmd/sekia-mcp@latest
```

### Docker

```bash
# Run the full stack
docker compose up

# Or just the daemon
docker compose up sekiad
```

Agent credentials are read from environment variables. Copy `.env.example` to `.env` and fill in your tokens:

```bash
cp .env.example .env
# Edit .env with your GITHUB_TOKEN, SLACK_BOT_TOKEN, etc.
docker compose up
```

Individual images can be built with targets:

```bash
docker build --target sekiad -t sekia/sekiad .
docker build --target sekia-github -t sekia/sekia-github .
```

## Getting started

### Build

```bash
go build ./cmd/sekiad ./cmd/sekiactl ./cmd/sekia-github ./cmd/sekia-slack ./cmd/sekia-linear ./cmd/sekia-gmail ./cmd/sekia-mcp
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

sekia uses TOML config files searched in `/etc/sekia`, `~/.config/sekia`, and `.`. Environment variables with the `SEKIA_` prefix are also supported.

Defaults:

| Key | Default |
|---|---|
| `server.socket` | `/tmp/sekiad.sock` |
| `server.listen` | `127.0.0.1:7600` |
| `nats.embedded` | `true` |
| `nats.data_dir` | `~/.local/share/sekia/nats` |
| `workflows.dir` | `~/.config/sekia/workflows` |
| `workflows.hot_reload` | `true` |
| `ai.provider` | `anthropic` |
| `ai.model` | `claude-sonnet-4-20250514` |
| `ai.max_tokens` | `1024` |
| `web.listen` | (empty — disabled) |

See [configs/sekia.toml](configs/sekia.toml) for an example.

## Web Dashboard

sekia includes an embedded web dashboard for monitoring agents, workflows, and live events. Enable it by setting `web.listen`:

```toml
[web]
listen = ":8080"
```

Then open `http://localhost:8080/web` in your browser. The dashboard shows:

- **System status** — uptime, NATS status, agent/workflow counts
- **Connected agents** — name, status, version, event/error counters, last heartbeat
- **Loaded workflows** — name, handler count, event/error counters, patterns
- **Live events** — real-time event stream via Server-Sent Events (SSE)

Status and agent/workflow panels auto-refresh every 5–10 seconds via htmx. The event stream updates in real-time. No external dependencies — htmx and Alpine.js are vendored and embedded in the binary.

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
			Version:      "0.0.7",
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
    if event.type ~= "github.issue.opened" then return end

    local title = string.lower(event.payload.title or "")
    local label = "triage"
    if string.find(title, "bug") then label = "bug" end

    sekia.command("github-agent", "add_label", {
        owner  = event.payload.owner,
        repo   = event.payload.repo,
        number = event.payload.number,
        label  = label,
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
| `sekia.ai(prompt [, opts])` | Call an LLM and return the response text. Options: `model`, `max_tokens`, `temperature`, `system` |
| `sekia.ai_json(prompt [, opts])` | Like `sekia.ai` but requests JSON and returns a parsed Lua table |
| `sekia.name` | The workflow's name (derived from filename) |

Workflows run in a sandboxed Lua VM with only `base`, `table`, `string`, and `math` libraries available. Dangerous functions (`os`, `io`, `debug`, `dofile`, `load`) are removed.

When `hot_reload` is enabled (default), editing or adding `.lua` files automatically reloads the affected workflows.

### AI-Powered Workflows

Workflows can call an LLM using `sekia.ai()` and `sekia.ai_json()`. Configure the AI provider in `sekia.toml`:

```toml
[ai]
# api_key can also be set via SEKIA_AI_API_KEY env var
api_key = ""
model = "claude-sonnet-4-20250514"
max_tokens = 1024
```

Both functions are synchronous and return `(result, nil)` on success or `(nil, error_string)` on failure. If no API key is configured, they return `nil, "AI not configured"`.

`sekia.ai(prompt, opts)` returns the raw response text. `sekia.ai_json(prompt, opts)` requests a JSON response and parses it into a Lua table.

Options (all optional): `model`, `max_tokens`, `temperature`, `system`.

**Example — AI issue classifier** ([configs/workflows/ai-issue-classifier.lua](configs/workflows/ai-issue-classifier.lua)):

```lua
sekia.on("sekia.events.github", function(event)
    if event.type ~= "github.issue.opened" then return end

    local prompt = "Classify this GitHub issue. Reply with exactly one word: bug, feature, question, or docs.\n\n"
        .. "Title: " .. (event.payload.title or "") .. "\n"
        .. "Body: " .. (event.payload.body or "")

    local result, err = sekia.ai(prompt, {
        max_tokens = 16,
        temperature = 0,
    })

    if err then
        sekia.log("error", "AI classification failed: " .. err)
        return
    end

    local label = string.lower(string.gsub(result, "%s+", ""))
    sekia.command("github-agent", "add_label", {
        owner  = event.payload.owner,
        repo   = event.payload.repo,
        number = event.payload.number,
        label  = label,
    })
end)
```

**Example — AI PR summary** ([configs/workflows/ai-pr-summary.lua](configs/workflows/ai-pr-summary.lua)):

```lua
sekia.on("sekia.events.github", function(event)
    if event.type ~= "github.pr.opened" then return end

    local result, err = sekia.ai(
        "Write a brief one-paragraph summary of this pull request.\n\n"
            .. "Title: " .. (event.payload.title or "") .. "\n"
            .. "Body: " .. (event.payload.body or ""),
        { system = "You are a helpful code review assistant. Be concise and technical." }
    )

    if err then return end

    sekia.command("github-agent", "create_comment", {
        owner  = event.payload.owner,
        repo   = event.payload.repo,
        number = event.payload.number,
        body   = "**AI Summary:** " .. result,
    })
end)
```

## Agents

Each agent is a standalone binary that connects to the daemon's NATS bus, publishes events from an external service, and executes commands dispatched by Lua workflows.

### GitHub Agent

Ingests GitHub events via webhooks and/or REST API polling, and executes GitHub API commands.

**Webhook mode** (default):

```bash
export GITHUB_TOKEN=ghp_...
./sekia-github
```

Point your GitHub repository's webhook settings to `http://<host>:8080/webhook`. Optionally set `GITHUB_WEBHOOK_SECRET` to verify signatures.

**Polling mode** — useful when the agent cannot receive inbound webhooks (e.g., behind a firewall or in local development). Both modes can run simultaneously.

```toml
# sekia-github.toml
[poll]
enabled = true
interval = "30s"
repos = ["myorg/myrepo"]
```

To use polling only (no webhook server), set `webhook.listen = ""`.

**Config**: [configs/sekia-github.toml](configs/sekia-github.toml). Env vars: `GITHUB_TOKEN`, `GITHUB_WEBHOOK_SECRET`, `SEKIA_NATS_URL`.

**Events**:

| GitHub Event | sekia Event Type | Source |
|---|---|---|
| Issue opened/closed/reopened/labeled/assigned | `github.issue.<action>` | Webhook |
| Issue opened/closed | `github.issue.opened`, `github.issue.closed` | Polling |
| Issue updated (any change) | `github.issue.updated` | Polling only |
| PR opened/closed/merged/review_requested | `github.pr.<action>` | Webhook |
| PR opened/closed/merged | `github.pr.opened`, `github.pr.closed`, `github.pr.merged` | Polling |
| PR updated (any change) | `github.pr.updated` | Polling only |
| Push | `github.push` | Webhook only |
| Issue comment created | `github.comment.created` | Both |

Polled events include `payload.polled = true` so workflows can distinguish them from webhook events if needed.

**Commands**:

| Command | Required Payload | Action |
|---|---|---|
| `add_label` | `owner`, `repo`, `number`, `label` | Add a label to an issue/PR |
| `remove_label` | `owner`, `repo`, `number`, `label` | Remove a label |
| `create_comment` | `owner`, `repo`, `number`, `body` | Post a comment |
| `close_issue` | `owner`, `repo`, `number` | Close an issue |
| `reopen_issue` | `owner`, `repo`, `number` | Reopen an issue |

**Example workflow**: [configs/workflows/github-auto-label.lua](configs/workflows/github-auto-label.lua)

---

### Slack Agent

Connects to Slack via [Socket Mode](https://api.slack.com/apis/socket-mode) (WebSocket). No public URL required.

```bash
export SLACK_BOT_TOKEN=xoxb-...
export SLACK_APP_TOKEN=xapp-...
./sekia-slack
```

**Setup**: Create a Slack app at [api.slack.com/apps](https://api.slack.com/apps) with Socket Mode enabled. Required bot token scopes: `chat:write`, `reactions:write`, `channels:history`, `groups:history`, `im:history`. Generate an app-level token with `connections:write` scope.

**Config**: [configs/sekia-slack.toml](configs/sekia-slack.toml). Env vars: `SLACK_BOT_TOKEN`, `SLACK_APP_TOKEN`, `SEKIA_NATS_URL`.

**Events**:

| Slack Event | sekia Event Type | Payload Fields |
|---|---|---|
| New message (not from bot) | `slack.message.received` | `channel`, `user`, `text`, `timestamp`, `thread_ts` (if threaded) |
| Reaction added | `slack.reaction.added` | `user`, `reaction`, `channel`, `timestamp` |
| Channel created | `slack.channel.created` | `channel_id`, `channel_name`, `creator` |
| Message mentioning the bot | `slack.mention` | `channel`, `user`, `text`, `timestamp` |

**Commands**:

| Command | Required Payload | Action |
|---|---|---|
| `send_message` | `channel`, `text` | Post a message to a channel |
| `add_reaction` | `channel`, `timestamp`, `emoji` | Add a reaction to a message |
| `send_reply` | `channel`, `thread_ts`, `text` | Reply in a thread |

**Example workflow**: [configs/workflows/slack-auto-reply.lua](configs/workflows/slack-auto-reply.lua)

```lua
sekia.on("sekia.events.slack", function(event)
    if event.type ~= "slack.mention" then return end

    sekia.command("slack-agent", "send_reply", {
        channel   = event.payload.channel,
        thread_ts = event.payload.timestamp,
        text      = "Hi <@" .. event.payload.user .. ">, thanks for reaching out!",
    })
end)
```

---

### Linear Agent

Polls the [Linear GraphQL API](https://developers.linear.app/docs/graphql/working-with-the-graphql-api) for updated issues and comments. No webhooks or public URL required.

```bash
export LINEAR_API_KEY=lin_api_...
./sekia-linear
```

**Setup**: Create a personal API key at [linear.app/settings/api](https://linear.app/settings/api).

**Config**: [configs/sekia-linear.toml](configs/sekia-linear.toml). Env vars: `LINEAR_API_KEY`, `SEKIA_NATS_URL`.

| Setting | Default | Description |
|---|---|---|
| `poll.interval` | `30s` | How often to poll Linear |
| `poll.team_filter` | (empty) | Limit to a team key (e.g., `ENG`) |

**Events**:

| Trigger | sekia Event Type | Payload Fields |
|---|---|---|
| New issue (created since last poll) | `linear.issue.created` | `id`, `identifier`, `title`, `state`, `priority`, `team`, `url`, `assignee`, `labels` |
| Issue updated | `linear.issue.updated` | (same as above) |
| Issue moved to Done/Completed/Canceled | `linear.issue.completed` | (same as above) |
| New comment | `linear.comment.created` | `id`, `body`, `author`, `issue_id`, `issue_identifier` |

**Commands**:

| Command | Required Payload | Action |
|---|---|---|
| `create_issue` | `team_id`, `title`, `description` (optional) | Create a new issue |
| `update_issue` | `issue_id`, plus `state_id`/`assignee_id`/`priority` | Update an issue |
| `create_comment` | `issue_id`, `body` | Add a comment to an issue |
| `add_label` | `issue_id`, `label_id` | Add a label to an issue |

**Example workflow**: [configs/workflows/linear-auto-triage.lua](configs/workflows/linear-auto-triage.lua)

```lua
sekia.on("sekia.events.linear", function(event)
    if event.type ~= "linear.issue.created" then return end

    sekia.command("linear-agent", "create_comment", {
        issue_id = event.payload.id,
        body     = "Auto-triaged. Team: " .. (event.payload.team or "unknown"),
    })
end)
```

---

### Gmail Agent

Polls Gmail via IMAP for new messages and sends emails via SMTP. No Google Cloud setup required.

```bash
export GMAIL_ADDRESS=you@gmail.com
export GMAIL_APP_PASSWORD=abcd-efgh-ijkl-mnop
./sekia-gmail
```

**Setup**: Generate an App Password at [myaccount.google.com/apppasswords](https://myaccount.google.com/apppasswords) (requires 2FA enabled). The app password is used for both IMAP reads and SMTP sends.

**Config**: [configs/sekia-gmail.toml](configs/sekia-gmail.toml). Env vars: `GMAIL_ADDRESS`, `GMAIL_APP_PASSWORD`, `SEKIA_NATS_URL`.

| Setting | Default | Description |
|---|---|---|
| `poll.interval` | `60s` | How often to check for new emails |
| `poll.folder` | `INBOX` | IMAP folder to poll |
| `imap.server` | `imap.gmail.com:993` | IMAP server address |
| `smtp.server` | `smtp.gmail.com:587` | SMTP server address |

**Events**:

| Trigger | sekia Event Type | Payload Fields |
|---|---|---|
| New unseen message | `gmail.message.received` | `uid`, `message_id`, `from`, `to`, `subject`, `body`, `date` |

**Commands**:

| Command | Required Payload | Action |
|---|---|---|
| `send_email` | `to`, `subject`, `body` | Send a new email |
| `reply_email` | `message_id`, `body` | Reply to an email (sets In-Reply-To header) |
| `add_label` | `message_uid`, `label` | Copy message to a Gmail label folder |
| `archive` | `message_uid` | Move message out of inbox to All Mail |

**Example workflow**: [configs/workflows/gmail-auto-reply.lua](configs/workflows/gmail-auto-reply.lua)

```lua
sekia.on("sekia.events.gmail", function(event)
    if event.type ~= "gmail.message.received" then return end

    local subject = string.lower(event.payload.subject or "")
    if string.find(subject, "urgent") then
        sekia.command("gmail-agent", "reply_email", {
            message_id = event.payload.message_id,
            body       = "Acknowledged. This has been flagged as urgent.",
        })
    end
end)
```

---

### MCP Server

Exposes sekia capabilities to AI assistants (Claude Desktop, Claude Code, Cursor) via the [Model Context Protocol](https://modelcontextprotocol.io). Uses stdio transport — the MCP client launches `sekia-mcp` as a subprocess.

```bash
./sekia-mcp
```

**Config**: [configs/sekia-mcp.toml](configs/sekia-mcp.toml). Env vars: `SEKIA_NATS_URL`, `SEKIA_DAEMON_SOCKET`.

**MCP Tools**:

| Tool | Description |
|---|---|
| `get_status` | Daemon health, uptime, NATS status, agent/workflow counts |
| `list_agents` | Connected agents with capabilities, commands, and heartbeat data |
| `list_workflows` | Loaded Lua workflows with handler patterns and event/error counts |
| `reload_workflows` | Hot-reload all .lua workflow files from disk |
| `publish_event` | Emit a synthetic event onto the NATS bus to trigger workflows |
| `send_command` | Send a command to a connected agent (Slack message, GitHub comment, etc.) |

**Claude Desktop setup**: Add to your MCP settings (`~/Library/Application Support/Claude/claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "sekia": {
      "command": "sekia-mcp",
      "env": {
        "SEKIA_NATS_URL": "nats://127.0.0.1:4222",
        "SEKIA_DAEMON_SOCKET": "/tmp/sekiad.sock"
      }
    }
  }
}
```

---

## Cross-Agent Workflows

The real power of sekia is connecting agents together. Here's a workflow that posts to Slack when a GitHub issue is opened, and creates a Linear tracking issue:

```lua
-- ~/.config/sekia/workflows/issue-tracker.lua

sekia.on("sekia.events.github", function(event)
    if event.type ~= "github.issue.opened" then return end

    local title = event.payload.title
    local url   = event.payload.url
    local repo  = event.payload.repo

    -- Notify the team in Slack
    sekia.command("slack-agent", "send_message", {
        channel = "C_ENGINEERING",
        text    = "New issue in " .. repo .. ": " .. title .. "\n" .. url,
    })

    -- Create a tracking issue in Linear
    sekia.command("linear-agent", "create_issue", {
        team_id     = "TEAM_ID_HERE",
        title       = "[" .. repo .. "] " .. title,
        description = "GitHub issue: " .. url,
    })
end)
```

## Testing

```bash
go test ./...
```

Each agent has end-to-end integration tests that start the full daemon with embedded NATS, connect the agent in-process, and verify the complete event-to-command flow through Lua workflows.

## License

Apache 2.0
