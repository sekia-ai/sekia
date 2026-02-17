package google

import (
	"github.com/sekia-ai/sekia/pkg/protocol"
)

// MapGmailEvent converts an EmailMessage into a sekia Event.
func MapGmailEvent(msg EmailMessage) protocol.Event {
	payload := map[string]any{
		"id":         msg.ID,
		"thread_id":  msg.ThreadID,
		"message_id": msg.MessageID,
		"from":       msg.From,
		"to":         msg.To,
		"subject":    msg.Subject,
		"body":       msg.Body,
		"snippet":    msg.Snippet,
		"date":       msg.Date,
		"labels":     msg.Labels,
	}

	return protocol.NewEvent("gmail.message.received", "google", payload)
}
