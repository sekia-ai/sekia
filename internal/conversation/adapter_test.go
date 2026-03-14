package conversation

import (
	"testing"
	"time"
)

func TestWorkflowAdapter(t *testing.T) {
	store := NewStore(50, 1*time.Hour)
	adapter := NewWorkflowAdapter(store)

	// GetOrCreateID should return a non-empty ID.
	id := adapter.GetOrCreateID("slack", "C123", "T456")
	if id == "" {
		t.Fatal("expected non-empty ID")
	}

	// Same key returns same ID.
	id2 := adapter.GetOrCreateID("slack", "C123", "T456")
	if id != id2 {
		t.Errorf("expected same ID, got %s vs %s", id, id2)
	}

	// AppendMessage + GetMessages.
	adapter.AppendMessage(id, "user", "hello")
	adapter.AppendMessage(id, "assistant", "hi")

	msgs := adapter.GetMessages(id)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "hello" {
		t.Errorf("unexpected first message: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "hi" {
		t.Errorf("unexpected second message: %+v", msgs[1])
	}

	// Metadata.
	adapter.SetMetadata(id, "key", "value")
	if v := adapter.GetMetadata(id, "key"); v != "value" {
		t.Errorf("expected 'value', got %q", v)
	}
}
