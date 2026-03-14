package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/sekia-ai/sekia/internal/ai"
	"github.com/sekia-ai/sekia/internal/conversation"
)

func TestLuaConversation_AppendAndHistory(t *testing.T) {
	_, nc := startTestNATS(t)

	store := conversation.NewStore(50, 1*time.Hour)
	adapter := conversation.NewWorkflowAdapter(store)

	L := NewSandboxedState("test-wf", testLogger())
	defer L.Close()

	ctx := &moduleContext{
		name:       "test-wf",
		nc:         nc,
		logger:     testLogger(),
		convoStore: adapter,
	}
	registerSekiaModule(L, ctx)

	err := L.DoString(`
		local conv = sekia.conversation("slack", "C123", "T456")
		conv:append("user", "hello")
		conv:append("assistant", "hi there")

		local history = conv:history()
		assert(#history == 2, "expected 2 messages, got " .. #history)
		assert(history[1].role == "user", "expected user, got " .. history[1].role)
		assert(history[1].content == "hello", "expected hello, got " .. history[1].content)
		assert(history[2].role == "assistant", "expected assistant")
		assert(history[2].content == "hi there", "expected hi there")
	`)
	if err != nil {
		t.Fatalf("DoString: %v", err)
	}
}

func TestLuaConversation_Metadata(t *testing.T) {
	_, nc := startTestNATS(t)

	store := conversation.NewStore(50, 1*time.Hour)
	adapter := conversation.NewWorkflowAdapter(store)

	L := NewSandboxedState("test-wf", testLogger())
	defer L.Close()

	ctx := &moduleContext{
		name:       "test-wf",
		nc:         nc,
		logger:     testLogger(),
		convoStore: adapter,
	}
	registerSekiaModule(L, ctx)

	err := L.DoString(`
		local conv = sekia.conversation("slack", "C123", "T456")
		conv:metadata("mood", "happy")
		local mood = conv:metadata("mood")
		assert(mood == "happy", "expected happy, got " .. tostring(mood))
	`)
	if err != nil {
		t.Fatalf("DoString: %v", err)
	}
}

func TestLuaConversation_Reply(t *testing.T) {
	_, nc := startTestNATS(t)

	store := conversation.NewStore(50, 1*time.Hour)
	adapter := conversation.NewWorkflowAdapter(store)

	// Create a mock LLM that returns a fixed response.
	mockLLM := &mockLLMClient{response: "I'm a helpful assistant!"}

	L := NewSandboxedState("test-wf", testLogger())
	defer L.Close()

	ctx := &moduleContext{
		name:       "test-wf",
		nc:         nc,
		logger:     testLogger(),
		llm:        mockLLM,
		convoStore: adapter,
	}
	registerSekiaModule(L, ctx)

	err := L.DoString(`
		local conv = sekia.conversation("slack", "C123", "T456")
		local response, err = conv:reply("What can you do?")
		assert(err == nil, "expected no error, got " .. tostring(err))
		assert(response == "I'm a helpful assistant!", "unexpected response: " .. tostring(response))

		-- Verify history includes both user prompt and assistant response.
		local history = conv:history()
		assert(#history == 2, "expected 2 messages, got " .. #history)
		assert(history[1].role == "user", "expected user")
		assert(history[2].role == "assistant", "expected assistant")
	`)
	if err != nil {
		t.Fatalf("DoString: %v", err)
	}

	// Verify the mock received the messages.
	if mockLLM.lastReq.Messages == nil {
		t.Fatal("expected messages in request")
	}
	if len(mockLLM.lastReq.Messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(mockLLM.lastReq.Messages))
	}
}

func TestLuaConversation_NotConfigured(t *testing.T) {
	_, nc := startTestNATS(t)

	L := NewSandboxedState("test-wf", testLogger())
	defer L.Close()

	ctx := &moduleContext{
		name:       "test-wf",
		nc:         nc,
		logger:     testLogger(),
		convoStore: nil, // not configured
	}
	registerSekiaModule(L, ctx)

	err := L.DoString(`
		local ok, msg = pcall(function()
			sekia.conversation("slack", "C123", "T456")
		end)
		assert(not ok, "expected error when conversations not configured")
	`)
	if err != nil {
		t.Fatalf("DoString: %v", err)
	}
}

// mockLLMClient is a test double for ai.LLMClient.
type mockLLMClient struct {
	response string
	lastReq  ai.CompleteRequest
}

func (m *mockLLMClient) Complete(_ context.Context, req ai.CompleteRequest) (string, error) {
	m.lastReq = req
	return m.response, nil
}
