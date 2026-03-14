package conversation

import (
	"time"

	"github.com/sekia-ai/sekia/internal/ai"
)

// WorkflowAdapter wraps a Store to implement the workflow.ConversationStore interface.
type WorkflowAdapter struct {
	store *Store
}

// NewWorkflowAdapter creates an adapter for the workflow engine.
func NewWorkflowAdapter(store *Store) *WorkflowAdapter {
	return &WorkflowAdapter{store: store}
}

// GetOrCreateID returns the conversation ID, creating the conversation if needed.
func (a *WorkflowAdapter) GetOrCreateID(platform, channelID, threadID string) string {
	c := a.store.GetOrCreate(platform, channelID, threadID)
	return c.ID
}

// AppendMessage adds a message to a conversation.
func (a *WorkflowAdapter) AppendMessage(convoID, role, content string) {
	a.store.Append(convoID, Message{
		Role:      role,
		Content:   content,
		Timestamp: time.Now(),
	})
}

// GetMessages returns messages for a conversation as ai.Message values.
func (a *WorkflowAdapter) GetMessages(convoID string) []ai.Message {
	msgs := a.store.Messages(convoID)
	result := make([]ai.Message, len(msgs))
	for i, m := range msgs {
		result[i] = ai.Message{Role: m.Role, Content: m.Content}
	}
	return result
}

// SetMetadata sets a metadata key/value on a conversation.
func (a *WorkflowAdapter) SetMetadata(convoID, key, value string) {
	a.store.SetMetadata(convoID, key, value)
}

// GetMetadata gets a metadata value from a conversation.
func (a *WorkflowAdapter) GetMetadata(convoID, key string) string {
	return a.store.GetMetadata(convoID, key)
}
