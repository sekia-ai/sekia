package workflow

import (
	"context"
	"fmt"
	"testing"

	"github.com/sekia-ai/sekia/internal/ai"
)

// mockLLM implements ai.LLMClient for testing.
type mockLLM struct {
	response string
	err      error
	lastReq  ai.CompleteRequest
}

func (m *mockLLM) Complete(_ context.Context, req ai.CompleteRequest) (string, error) {
	m.lastReq = req
	return m.response, m.err
}

func TestLuaAI_Success(t *testing.T) {
	_, nc := startTestNATS(t)

	mock := &mockLLM{response: "bug"}

	L := NewSandboxedState("test-wf", testLogger())
	defer L.Close()

	ctx := &moduleContext{
		name:   "test-wf",
		nc:     nc,
		logger: testLogger(),
		llm:    mock,
	}
	registerSekiaModule(L, ctx)

	err := L.DoString(`
		local result, err = sekia.ai("classify this issue")
		assert(result == "bug", "expected bug, got " .. tostring(result))
		assert(err == nil, "expected nil error, got " .. tostring(err))
	`)
	if err != nil {
		t.Fatalf("DoString: %v", err)
	}

	if mock.lastReq.Prompt != "classify this issue" {
		t.Errorf("prompt = %q, want %q", mock.lastReq.Prompt, "classify this issue")
	}
}

func TestLuaAI_WithOptions(t *testing.T) {
	_, nc := startTestNATS(t)

	mock := &mockLLM{response: "ok"}

	L := NewSandboxedState("test-wf", testLogger())
	defer L.Close()

	ctx := &moduleContext{
		name:   "test-wf",
		nc:     nc,
		logger: testLogger(),
		llm:    mock,
	}
	registerSekiaModule(L, ctx)

	err := L.DoString(`
		local result, err = sekia.ai("test", {
			model = "custom-model",
			max_tokens = 256,
			temperature = 0.5,
			system = "be concise",
		})
		assert(result == "ok", "expected ok, got " .. tostring(result))
	`)
	if err != nil {
		t.Fatalf("DoString: %v", err)
	}

	if mock.lastReq.Model != "custom-model" {
		t.Errorf("model = %q, want %q", mock.lastReq.Model, "custom-model")
	}
	if mock.lastReq.MaxTokens != 256 {
		t.Errorf("max_tokens = %d, want %d", mock.lastReq.MaxTokens, 256)
	}
	if mock.lastReq.Temperature != 0.5 {
		t.Errorf("temperature = %f, want %f", mock.lastReq.Temperature, 0.5)
	}
	if mock.lastReq.SystemPrompt != "be concise" {
		t.Errorf("system = %q, want %q", mock.lastReq.SystemPrompt, "be concise")
	}
}

func TestLuaAI_Error(t *testing.T) {
	_, nc := startTestNATS(t)

	mock := &mockLLM{err: fmt.Errorf("rate limited")}

	L := NewSandboxedState("test-wf", testLogger())
	defer L.Close()

	ctx := &moduleContext{
		name:   "test-wf",
		nc:     nc,
		logger: testLogger(),
		llm:    mock,
	}
	registerSekiaModule(L, ctx)

	err := L.DoString(`
		local result, err = sekia.ai("test")
		assert(result == nil, "expected nil result, got " .. tostring(result))
		assert(err == "rate limited", "expected 'rate limited', got " .. tostring(err))
	`)
	if err != nil {
		t.Fatalf("DoString: %v", err)
	}
}

func TestLuaAI_NotConfigured(t *testing.T) {
	_, nc := startTestNATS(t)

	L := NewSandboxedState("test-wf", testLogger())
	defer L.Close()

	ctx := &moduleContext{
		name:   "test-wf",
		nc:     nc,
		logger: testLogger(),
		llm:    nil, // no LLM configured
	}
	registerSekiaModule(L, ctx)

	err := L.DoString(`
		local result, err = sekia.ai("test")
		assert(result == nil, "expected nil result")
		assert(string.find(err, "not configured"), "expected 'not configured' in error, got " .. tostring(err))
	`)
	if err != nil {
		t.Fatalf("DoString: %v", err)
	}
}

func TestLuaAIJSON_Success(t *testing.T) {
	_, nc := startTestNATS(t)

	mock := &mockLLM{response: `{"label":"bug","confidence":0.95}`}

	L := NewSandboxedState("test-wf", testLogger())
	defer L.Close()

	ctx := &moduleContext{
		name:   "test-wf",
		nc:     nc,
		logger: testLogger(),
		llm:    mock,
	}
	registerSekiaModule(L, ctx)

	err := L.DoString(`
		local result, err = sekia.ai_json("classify this")
		assert(err == nil, "expected nil error, got " .. tostring(err))
		assert(type(result) == "table", "expected table, got " .. type(result))
		assert(result.label == "bug", "expected bug, got " .. tostring(result.label))
		assert(result.confidence == 0.95, "expected 0.95, got " .. tostring(result.confidence))
	`)
	if err != nil {
		t.Fatalf("DoString: %v", err)
	}

	if !mock.lastReq.JSONMode {
		t.Error("expected JSONMode to be true")
	}
}

func TestLuaAIJSON_InvalidJSON(t *testing.T) {
	_, nc := startTestNATS(t)

	mock := &mockLLM{response: "this is not json"}

	L := NewSandboxedState("test-wf", testLogger())
	defer L.Close()

	ctx := &moduleContext{
		name:   "test-wf",
		nc:     nc,
		logger: testLogger(),
		llm:    mock,
	}
	registerSekiaModule(L, ctx)

	err := L.DoString(`
		local result, err = sekia.ai_json("test")
		assert(result == nil, "expected nil result")
		assert(string.find(err, "invalid JSON"), "expected 'invalid JSON' in error, got " .. tostring(err))
	`)
	if err != nil {
		t.Fatalf("DoString: %v", err)
	}
}

func TestLuaAIJSON_NotConfigured(t *testing.T) {
	_, nc := startTestNATS(t)

	L := NewSandboxedState("test-wf", testLogger())
	defer L.Close()

	ctx := &moduleContext{
		name:   "test-wf",
		nc:     nc,
		logger: testLogger(),
		llm:    nil,
	}
	registerSekiaModule(L, ctx)

	err := L.DoString(`
		local result, err = sekia.ai_json("test")
		assert(result == nil, "expected nil result")
		assert(string.find(err, "not configured"), "expected 'not configured' in error, got " .. tostring(err))
	`)
	if err != nil {
		t.Fatalf("DoString: %v", err)
	}
}
