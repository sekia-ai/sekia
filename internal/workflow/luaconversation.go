package workflow

import (
	"context"
	"time"

	lua "github.com/yuin/gopher-lua"

	"github.com/sekia-ai/sekia/internal/ai"
)

// luaConversation implements sekia.conversation(platform, channel, thread) -> table
// Returns a Lua table with methods: :append(role, content), :reply(prompt), :history(), :metadata(key, [value])
func (ctx *moduleContext) luaConversation(L *lua.LState) int {
	if ctx.convoStore == nil {
		L.RaiseError("conversations not configured: add [conversation] section to sekia.toml")
		return 0
	}

	platform := L.CheckString(1)
	channelID := L.CheckString(2)
	threadID := L.OptString(3, "")

	convoID := ctx.convoStore.GetOrCreateID(platform, channelID, threadID)

	// Build a Lua table representing the conversation handle.
	conv := L.NewTable()

	// conv:append(role, content)
	L.SetField(conv, "append", L.NewFunction(func(L *lua.LState) int {
		role := L.CheckString(2)    // 1 is self
		content := L.CheckString(3) // 2 is role, 3 is content
		ctx.convoStore.AppendMessage(convoID, role, content)
		return 0
	}))

	// conv:reply(prompt) -> response, err
	L.SetField(conv, "reply", L.NewFunction(func(L *lua.LState) int {
		prompt := L.CheckString(2) // 1 is self

		if ctx.llm == nil {
			L.Push(lua.LNil)
			L.Push(lua.LString("AI not configured"))
			return 2
		}

		// Build messages from conversation history + new prompt.
		ctx.convoStore.AppendMessage(convoID, "user", prompt)

		req := ai.CompleteRequest{
			Messages:    ctx.convoStore.GetMessages(convoID),
			Temperature: -1,
		}
		ctx.injectSkillsIndex(&req)

		callCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		result, err := ctx.llm.Complete(callCtx, req)
		if err != nil {
			ctx.logger.Error().Err(err).Msg("conversation reply failed")
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}

		// Append assistant response to conversation.
		ctx.convoStore.AppendMessage(convoID, "assistant", result)

		L.Push(lua.LString(result))
		L.Push(lua.LNil)
		return 2
	}))

	// conv:history() -> table of {role=..., content=...}
	L.SetField(conv, "history", L.NewFunction(func(innerL *lua.LState) int {
		msgs := ctx.convoStore.GetMessages(convoID)
		tbl := innerL.NewTable()
		for i, m := range msgs {
			entry := innerL.NewTable()
			innerL.SetField(entry, "role", lua.LString(m.Role))
			innerL.SetField(entry, "content", lua.LString(m.Content))
			tbl.RawSetInt(i+1, entry)
		}
		innerL.Push(tbl)
		return 1
	}))

	// conv:metadata(key, [value]) -> get or set metadata
	L.SetField(conv, "metadata", L.NewFunction(func(L *lua.LState) int {
		key := L.CheckString(2) // 1 is self
		if L.GetTop() >= 3 {
			value := L.CheckString(3)
			ctx.convoStore.SetMetadata(convoID, key, value)
			return 0
		}
		L.Push(lua.LString(ctx.convoStore.GetMetadata(convoID, key)))
		return 1
	}))

	L.Push(conv)
	return 1
}
