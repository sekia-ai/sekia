package protocol

import (
	"time"

	"github.com/google/uuid"
)

// Event is the canonical event envelope published on sekia.events.<source>.
type Event struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Source    string         `json:"source"`
	Timestamp int64          `json:"timestamp"`
	Payload   map[string]any `json:"payload"`
}

// NewEvent creates an Event with a generated ID and current timestamp.
func NewEvent(eventType, source string, payload map[string]any) Event {
	return Event{
		ID:        "evt_" + uuid.NewString(),
		Type:      eventType,
		Source:    source,
		Timestamp: time.Now().Unix(),
		Payload:   payload,
	}
}
