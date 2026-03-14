package conversation

import (
	"testing"
	"time"
)

func TestStore_GetOrCreate(t *testing.T) {
	s := NewStore(50, 1*time.Hour)

	c1 := s.GetOrCreate("slack", "C123", "T456")
	if c1 == nil {
		t.Fatal("expected conversation, got nil")
	}
	if c1.Platform != "slack" || c1.ChannelID != "C123" || c1.ThreadID != "T456" {
		t.Errorf("unexpected fields: %+v", c1)
	}

	// Same key returns same conversation.
	c2 := s.GetOrCreate("slack", "C123", "T456")
	if c1.ID != c2.ID {
		t.Errorf("expected same conversation, got different IDs: %s vs %s", c1.ID, c2.ID)
	}

	// Different key returns different conversation.
	c3 := s.GetOrCreate("slack", "C123", "T789")
	if c1.ID == c3.ID {
		t.Error("expected different conversation for different thread")
	}
}

func TestStore_AppendAndMessages(t *testing.T) {
	s := NewStore(50, 1*time.Hour)

	c := s.GetOrCreate("slack", "C1", "")
	s.Append(c.ID, Message{Role: "user", Content: "hello"})
	s.Append(c.ID, Message{Role: "assistant", Content: "hi there"})

	msgs := s.Messages(c.ID)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "hello" {
		t.Errorf("unexpected first message: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "hi there" {
		t.Errorf("unexpected second message: %+v", msgs[1])
	}
}

func TestStore_MaxHistory(t *testing.T) {
	s := NewStore(3, 1*time.Hour)

	c := s.GetOrCreate("test", "ch", "")
	for i := 0; i < 5; i++ {
		s.Append(c.ID, Message{Role: "user", Content: "msg"})
	}

	msgs := s.Messages(c.ID)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages after trimming, got %d", len(msgs))
	}
}

func TestStore_Metadata(t *testing.T) {
	s := NewStore(50, 1*time.Hour)

	c := s.GetOrCreate("test", "ch", "")
	s.SetMetadata(c.ID, "mood", "happy")

	got := s.GetMetadata(c.ID, "mood")
	if got != "happy" {
		t.Errorf("expected 'happy', got %q", got)
	}

	// Non-existent key returns empty string.
	if v := s.GetMetadata(c.ID, "missing"); v != "" {
		t.Errorf("expected empty string for missing key, got %q", v)
	}
}

func TestStore_Cleanup(t *testing.T) {
	s := NewStore(50, 100*time.Millisecond)

	c := s.GetOrCreate("test", "ch", "")
	s.Append(c.ID, Message{Role: "user", Content: "hello"})

	// No cleanup yet — conversation is fresh.
	if removed := s.Cleanup(); removed != 0 {
		t.Errorf("expected 0 removed, got %d", removed)
	}

	time.Sleep(150 * time.Millisecond)

	if removed := s.Cleanup(); removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}

	if s.Count() != 0 {
		t.Errorf("expected 0 conversations after cleanup, got %d", s.Count())
	}
}

func TestStore_MessagesReturnsCopy(t *testing.T) {
	s := NewStore(50, 1*time.Hour)

	c := s.GetOrCreate("test", "ch", "")
	s.Append(c.ID, Message{Role: "user", Content: "hello"})

	msgs := s.Messages(c.ID)
	msgs[0].Content = "mutated"

	// Original should not be affected.
	original := s.Messages(c.ID)
	if original[0].Content != "hello" {
		t.Error("Messages() did not return a copy")
	}
}

func TestStore_NonExistentConversation(t *testing.T) {
	s := NewStore(50, 1*time.Hour)

	msgs := s.Messages("nonexistent")
	if msgs != nil {
		t.Errorf("expected nil for nonexistent conversation, got %v", msgs)
	}

	// SetMetadata and GetMetadata on nonexistent should not panic.
	s.SetMetadata("nonexistent", "key", "value")
	if v := s.GetMetadata("nonexistent", "key"); v != "" {
		t.Errorf("expected empty string, got %q", v)
	}
}
