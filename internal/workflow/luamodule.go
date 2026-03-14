package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	lua "github.com/yuin/gopher-lua"

	"github.com/sekia-ai/sekia/internal/ai"
	"github.com/sekia-ai/sekia/pkg/protocol"
)

// handlerEntry binds a NATS subject pattern to a Lua callback.
type handlerEntry struct {
	Pattern string
	Fn      *lua.LFunction
}

// moduleContext holds the state shared between Lua module functions and the Go engine.
// Each workflow gets its own moduleContext.
type moduleContext struct {
	name          string
	nc            *nats.Conn
	logger        zerolog.Logger
	handlers      []handlerEntry
	schedules     []scheduleEntry
	llm           ai.LLMClient          // nil if AI is not configured
	commandSecret string                 // HMAC-SHA256 secret for signing commands (empty = no signing)
	skillsIndex   string                 // compact skills summary for AI prompts
	skillResolver SkillResolver          // resolves full skill instructions by name
	convoStore    ConversationStore      // conversation store (nil if not configured)
}

// ConversationStore is the interface the workflow engine uses for conversation state.
type ConversationStore interface {
	GetOrCreateID(platform, channelID, threadID string) string
	AppendMessage(convoID, role, content string)
	GetMessages(convoID string) []ai.Message
	SetMetadata(convoID, key, value string)
	GetMetadata(convoID, key string) string
}

// registerSekiaModule creates the global "sekia" table with on/publish/command/log functions.
func registerSekiaModule(L *lua.LState, ctx *moduleContext) {
	mod := L.NewTable()

	L.SetField(mod, "name", lua.LString(ctx.name))
	L.SetField(mod, "on", L.NewFunction(ctx.luaOn))
	L.SetField(mod, "publish", L.NewFunction(ctx.luaPublish))
	L.SetField(mod, "command", L.NewFunction(ctx.luaCommand))
	L.SetField(mod, "log", L.NewFunction(ctx.luaLog))
	L.SetField(mod, "ai", L.NewFunction(ctx.luaAI))
	L.SetField(mod, "ai_json", L.NewFunction(ctx.luaAIJSON))
	L.SetField(mod, "skill", L.NewFunction(ctx.luaSkill))
	L.SetField(mod, "conversation", L.NewFunction(ctx.luaConversation))
	L.SetField(mod, "schedule", L.NewFunction(ctx.luaSchedule))

	L.SetGlobal("sekia", mod)
}

// luaOn registers an event handler: sekia.on(pattern, handler)
func (ctx *moduleContext) luaOn(L *lua.LState) int {
	pattern := L.CheckString(1)
	fn := L.CheckFunction(2)

	ctx.handlers = append(ctx.handlers, handlerEntry{
		Pattern: pattern,
		Fn:      fn,
	})

	ctx.logger.Debug().
		Str("pattern", pattern).
		Msg("registered event handler")

	return 0
}

// luaPublish publishes an event: sekia.publish(subject, event_type, payload)
func (ctx *moduleContext) luaPublish(L *lua.LState) int {
	subject := L.CheckString(1)
	eventType := L.CheckString(2)
	payloadTbl := L.CheckTable(3)

	payloadRaw := TableToMap(payloadTbl)
	payload, ok := payloadRaw.(map[string]any)
	if !ok {
		L.ArgError(3, "expected a table with string keys")
		return 0
	}

	ev := protocol.NewEvent(eventType, fmt.Sprintf("workflow:%s", ctx.name), payload)
	data, err := json.Marshal(ev)
	if err != nil {
		L.RaiseError("marshal event: %s", err)
		return 0
	}

	if err := ctx.nc.Publish(subject, data); err != nil {
		L.RaiseError("publish event: %s", err)
		return 0
	}

	ctx.logger.Debug().
		Str("subject", subject).
		Str("event_type", eventType).
		Msg("published event")

	return 0
}

// luaCommand sends a command to an agent: sekia.command(agent_name, command, payload)
func (ctx *moduleContext) luaCommand(L *lua.LState) int {
	agentName := L.CheckString(1)
	command := L.CheckString(2)
	payloadTbl := L.CheckTable(3)

	payloadRaw := TableToMap(payloadTbl)
	payload, ok := payloadRaw.(map[string]any)
	if !ok {
		L.ArgError(3, "expected a table with string keys")
		return 0
	}

	cmd := &protocol.Command{
		Command: command,
		Payload: payload,
		Source:  fmt.Sprintf("workflow:%s", ctx.name),
	}
	if err := protocol.SignCommand(cmd, ctx.commandSecret); err != nil {
		L.RaiseError("sign command: %s", err)
		return 0
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		L.RaiseError("marshal command: %s", err)
		return 0
	}

	subject := protocol.SubjectCommands(agentName)
	if err := ctx.nc.Publish(subject, data); err != nil {
		L.RaiseError("publish command: %s", err)
		return 0
	}

	ctx.logger.Debug().
		Str("agent", agentName).
		Str("command", command).
		Msg("sent command")

	return 0
}

// luaSkill returns the full instructions for a named skill: sekia.skill(name) -> string
func (ctx *moduleContext) luaSkill(L *lua.LState) int {
	name := L.CheckString(1)
	if ctx.skillResolver == nil {
		L.Push(lua.LString(""))
		return 1
	}
	instructions := ctx.skillResolver.FullInstructions(name)
	L.Push(lua.LString(instructions))
	return 1
}

// luaSchedule registers a timer-driven handler: sekia.schedule(interval_seconds, handler)
func (ctx *moduleContext) luaSchedule(L *lua.LState) int {
	intervalSec := L.CheckNumber(1)
	fn := L.CheckFunction(2)

	interval := time.Duration(float64(intervalSec) * float64(time.Second))
	if interval < 1*time.Second {
		L.ArgError(1, "interval must be at least 1 second")
		return 0
	}

	ctx.schedules = append(ctx.schedules, scheduleEntry{
		Interval: interval,
		Fn:       fn,
	})

	ctx.logger.Debug().
		Dur("interval", interval).
		Msg("registered schedule handler")

	return 0
}

// luaLog logs a message: sekia.log(level, message)
func (ctx *moduleContext) luaLog(L *lua.LState) int {
	level := L.CheckString(1)
	message := L.CheckString(2)

	switch strings.ToLower(level) {
	case "debug":
		ctx.logger.Debug().Msg(message)
	case "info":
		ctx.logger.Info().Msg(message)
	case "warn":
		ctx.logger.Warn().Msg(message)
	case "error":
		ctx.logger.Error().Msg(message)
	default:
		ctx.logger.Info().Msg(message)
	}

	return 0
}
