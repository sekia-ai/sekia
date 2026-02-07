package gmail

import (
	"context"
	"fmt"
)

// extractString extracts a required string field from the payload.
func extractString(payload map[string]any, key string) (string, error) {
	val, ok := payload[key]
	if !ok {
		return "", fmt.Errorf("missing required field: %s", key)
	}
	s, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return s, nil
}

// extractUint32 extracts a required numeric field from the payload.
func extractUint32(payload map[string]any, key string) (uint32, error) {
	val, ok := payload[key]
	if !ok {
		return 0, fmt.Errorf("missing required field: %s", key)
	}
	switch n := val.(type) {
	case float64:
		return uint32(n), nil
	case int:
		return uint32(n), nil
	default:
		return 0, fmt.Errorf("%s must be a number", key)
	}
}

func cmdSendEmail(ctx context.Context, gc GmailClient, payload map[string]any) error {
	to, err := extractString(payload, "to")
	if err != nil {
		return err
	}
	subject, err := extractString(payload, "subject")
	if err != nil {
		return err
	}
	body, err := extractString(payload, "body")
	if err != nil {
		return err
	}
	return gc.SendEmail(ctx, to, subject, body)
}

func cmdReplyEmail(ctx context.Context, gc GmailClient, payload map[string]any) error {
	messageID, err := extractString(payload, "message_id")
	if err != nil {
		return err
	}
	body, err := extractString(payload, "body")
	if err != nil {
		return err
	}
	return gc.ReplyEmail(ctx, messageID, body)
}

func cmdAddLabel(ctx context.Context, gc GmailClient, payload map[string]any) error {
	messageUID, err := extractUint32(payload, "message_uid")
	if err != nil {
		return err
	}
	label, err := extractString(payload, "label")
	if err != nil {
		return err
	}
	return gc.AddLabel(ctx, messageUID, label)
}

func cmdArchive(ctx context.Context, gc GmailClient, payload map[string]any) error {
	messageUID, err := extractUint32(payload, "message_uid")
	if err != nil {
		return err
	}
	return gc.Archive(ctx, messageUID)
}
