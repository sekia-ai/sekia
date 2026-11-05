package google

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

// extractOptionalString extracts an optional string field from the payload.
func extractOptionalString(payload map[string]any, key string) string {
	val, ok := payload[key]
	if !ok {
		return ""
	}
	s, _ := val.(string)
	return s
}

// --- Gmail commands ---

func cmdGmailSendEmail(ctx context.Context, gc GmailClient, userID string, payload map[string]any) error {
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
	return gc.SendEmail(ctx, userID, to, subject, body)
}

func cmdGmailReplyEmail(ctx context.Context, gc GmailClient, userID string, payload map[string]any) error {
	threadID, err := extractString(payload, "thread_id")
	if err != nil {
		return err
	}
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
	inReplyTo := extractOptionalString(payload, "in_reply_to")
	return gc.ReplyEmail(ctx, userID, threadID, inReplyTo, to, subject, body)
}

func cmdGmailAddLabel(ctx context.Context, gc GmailClient, userID string, payload map[string]any) error {
	messageID, err := extractString(payload, "message_id")
	if err != nil {
		return err
	}
	label, err := extractString(payload, "label")
	if err != nil {
		return err
	}
	return gc.AddLabel(ctx, userID, messageID, label)
}

func cmdGmailRemoveLabel(ctx context.Context, gc GmailClient, userID string, payload map[string]any) error {
	messageID, err := extractString(payload, "message_id")
	if err != nil {
		return err
	}
	label, err := extractString(payload, "label")
	if err != nil {
		return err
	}
	return gc.RemoveLabel(ctx, userID, messageID, label)
}

func cmdGmailArchive(ctx context.Context, gc GmailClient, userID string, payload map[string]any) error {
	messageID, err := extractString(payload, "message_id")
	if err != nil {
		return err
	}
	return gc.Archive(ctx, userID, messageID)
}

func cmdGmailTrash(ctx context.Context, gc GmailClient, userID string, payload map[string]any) error {
	messageID, err := extractString(payload, "message_id")
	if err != nil {
		return err
	}
	return gc.Trash(ctx, userID, messageID)
}

func cmdGmailUntrash(ctx context.Context, gc GmailClient, userID string, payload map[string]any) error {
	messageID, err := extractString(payload, "message_id")
	if err != nil {
		return err
	}
	return gc.Untrash(ctx, userID, messageID)
}

func cmdGmailDelete(ctx context.Context, gc GmailClient, userID string, payload map[string]any) error {
	messageID, err := extractString(payload, "message_id")
	if err != nil {
		return err
	}
	return gc.Delete(ctx, userID, messageID)
}
