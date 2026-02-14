package google

import (
	"context"
	"fmt"
	"time"
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

// extractStringSlice extracts an optional string slice from the payload.
func extractStringSlice(payload map[string]any, key string) []string {
	val, ok := payload[key]
	if !ok {
		return nil
	}
	switch v := val.(type) {
	case []any:
		var result []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case []string:
		return v
	default:
		return nil
	}
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

// --- Calendar commands ---

func cmdCalendarCreateEvent(ctx context.Context, cc CalendarClient, calendarID string, payload map[string]any) error {
	summary, err := extractString(payload, "summary")
	if err != nil {
		return err
	}
	startStr, err := extractString(payload, "start")
	if err != nil {
		return err
	}
	endStr, err := extractString(payload, "end")
	if err != nil {
		return err
	}

	start, err := parseTime(startStr)
	if err != nil {
		return fmt.Errorf("parse start: %w", err)
	}
	end, err := parseTime(endStr)
	if err != nil {
		return fmt.Errorf("parse end: %w", err)
	}

	ev := CalendarEvent{
		Summary:     summary,
		Description: extractOptionalString(payload, "description"),
		Location:    extractOptionalString(payload, "location"),
		Start:       start,
		End:         end,
		Attendees:   extractStringSlice(payload, "attendees"),
	}

	_, err = cc.CreateEvent(ctx, calendarID, ev)
	return err
}

func cmdCalendarUpdateEvent(ctx context.Context, cc CalendarClient, calendarID string, payload map[string]any) error {
	eventID, err := extractString(payload, "event_id")
	if err != nil {
		return err
	}

	updates := make(map[string]any)
	for _, key := range []string{"summary", "description", "location", "start", "end"} {
		if v, ok := payload[key]; ok {
			updates[key] = v
		}
	}

	return cc.UpdateEvent(ctx, calendarID, eventID, updates)
}

func cmdCalendarDeleteEvent(ctx context.Context, cc CalendarClient, calendarID string, payload map[string]any) error {
	eventID, err := extractString(payload, "event_id")
	if err != nil {
		return err
	}
	return cc.DeleteEvent(ctx, calendarID, eventID)
}

func parseTime(s string) (time.Time, error) {
	// Try RFC3339 first, then date-only.
	t, err := time.Parse(time.RFC3339, s)
	if err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", s)
}
