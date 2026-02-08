package workflow

import (
	"context"
	"encoding/json"
	"time"

	lua "github.com/yuin/gopher-lua"

	"github.com/sekia-ai/sekia/internal/ai"
)

// luaAI implements sekia.ai(prompt) and sekia.ai(prompt, opts) -> result, err
func (ctx *moduleContext) luaAI(L *lua.LState) int {
	if ctx.llm == nil {
		L.Push(lua.LNil)
		L.Push(lua.LString("AI not configured: add [ai] section to sekia.toml"))
		return 2
	}

	prompt := L.CheckString(1)
	req := completeRequestFromLua(L, prompt)

	callCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result, err := ctx.llm.Complete(callCtx, req)
	if err != nil {
		ctx.logger.Error().Err(err).Msg("sekia.ai() call failed")
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}

	L.Push(lua.LString(result))
	L.Push(lua.LNil)
	return 2
}

// luaAIJSON implements sekia.ai_json(prompt, opts) -> table, err
func (ctx *moduleContext) luaAIJSON(L *lua.LState) int {
	if ctx.llm == nil {
		L.Push(lua.LNil)
		L.Push(lua.LString("AI not configured: add [ai] section to sekia.toml"))
		return 2
	}

	prompt := L.CheckString(1)
	req := completeRequestFromLua(L, prompt)
	req.JSONMode = true

	callCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result, err := ctx.llm.Complete(callCtx, req)
	if err != nil {
		ctx.logger.Error().Err(err).Msg("sekia.ai_json() call failed")
		L.Push(lua.LNil)
		L.Push(lua.LString(err.Error()))
		return 2
	}

	var parsed any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		L.Push(lua.LNil)
		L.Push(lua.LString("AI returned invalid JSON: " + err.Error()))
		return 2
	}

	L.Push(GoToLua(L, parsed))
	L.Push(lua.LNil)
	return 2
}

// completeRequestFromLua builds an ai.CompleteRequest from Lua arguments.
// Arg 1 is the prompt string (already extracted). Arg 2 is an optional options table.
func completeRequestFromLua(L *lua.LState, prompt string) ai.CompleteRequest {
	req := ai.CompleteRequest{
		Prompt:      prompt,
		Temperature: -1, // sentinel: use config default
	}

	if L.GetTop() < 2 {
		return req
	}

	opts := L.CheckTable(2)

	if v := L.GetField(opts, "model"); v != lua.LNil {
		if s, ok := v.(lua.LString); ok {
			req.Model = string(s)
		}
	}
	if v := L.GetField(opts, "max_tokens"); v != lua.LNil {
		if n, ok := v.(lua.LNumber); ok {
			req.MaxTokens = int(n)
		}
	}
	if v := L.GetField(opts, "temperature"); v != lua.LNil {
		if n, ok := v.(lua.LNumber); ok {
			req.Temperature = float64(n)
		}
	}
	if v := L.GetField(opts, "system"); v != lua.LNil {
		if s, ok := v.(lua.LString); ok {
			req.SystemPrompt = string(s)
		}
	}

	return req
}
