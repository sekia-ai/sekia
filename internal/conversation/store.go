package conversation

import (
	"fmt"
	"sync"
	"time"
)

// Message represents a single message in a conversation.
type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

// Conversation tracks a multi-turn conversation keyed by platform/channel/thread.
type Conversation struct {
	ID        string            `json:"id"`
	Platform  string            `json:"platform"`
	ChannelID string            `json:"channel_id"`
	ThreadID  string            `json:"thread_id"`
	Messages  []Message         `json:"messages"`
	Metadata  map[string]string `json:"metadata"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// Store manages in-memory conversation state.
type Store struct {
	mu         sync.RWMutex
	convos     map[string]*Conversation
	maxHistory int
	ttl        time.Duration
}

// NewStore creates a conversation store with the given limits.
func NewStore(maxHistory int, ttl time.Duration) *Store {
	return &Store{
		convos:     make(map[string]*Conversation),
		maxHistory: maxHistory,
		ttl:        ttl,
	}
}

// GetOrCreate returns an existing conversation or creates a new one.
func (s *Store) GetOrCreate(platform, channelID, threadID string) *Conversation {
	key := convoKey(platform, channelID, threadID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if c, ok := s.convos[key]; ok {
		return c
	}

	c := &Conversation{
		ID:        key,
		Platform:  platform,
		ChannelID: channelID,
		ThreadID:  threadID,
		Metadata:  make(map[string]string),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	s.convos[key] = c
	return c
}

// Append adds a message to a conversation, enforcing maxHistory.
func (s *Store) Append(convoID string, msg Message) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.convos[convoID]
	if !ok {
		return
	}

	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}

	c.Messages = append(c.Messages, msg)
	c.UpdatedAt = time.Now()

	// Trim to max history.
	if s.maxHistory > 0 && len(c.Messages) > s.maxHistory {
		c.Messages = c.Messages[len(c.Messages)-s.maxHistory:]
	}
}

// Messages returns a copy of messages for a conversation.
func (s *Store) Messages(convoID string) []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.convos[convoID]
	if !ok {
		return nil
	}

	msgs := make([]Message, len(c.Messages))
	copy(msgs, c.Messages)
	return msgs
}

// SetMetadata sets a metadata key/value on a conversation.
func (s *Store) SetMetadata(convoID, key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if c, ok := s.convos[convoID]; ok {
		c.Metadata[key] = value
	}
}

// GetMetadata gets a metadata value from a conversation.
func (s *Store) GetMetadata(convoID, key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if c, ok := s.convos[convoID]; ok {
		return c.Metadata[key]
	}
	return ""
}

// Cleanup removes conversations that have exceeded their TTL.
func (s *Store) Cleanup() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	cutoff := time.Now().Add(-s.ttl)
	removed := 0
	for key, c := range s.convos {
		if c.UpdatedAt.Before(cutoff) {
			delete(s.convos, key)
			removed++
		}
	}
	return removed
}

// Count returns the number of active conversations.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.convos)
}

func convoKey(platform, channelID, threadID string) string {
	return fmt.Sprintf("%s:%s:%s", platform, channelID, threadID)
}
