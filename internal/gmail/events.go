package gmail

import (
	"github.com/sekia-ai/sekia/pkg/protocol"
)

// MapEmailEvent converts an EmailMessage into a sekia Event.
func MapEmailEvent(msg EmailMessage) protocol.Event {
	payload := map[string]any{
		"uid":        msg.UID,
		"message_id": msg.MessageID,
		"from":       msg.From,
		"to":         msg.To,
		"subject":    msg.Subject,
		"body":       msg.Body,
		"date":       msg.Date,
	}

	return protocol.NewEvent("gmail.message.received", "gmail", payload)
}
