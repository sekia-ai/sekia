package google

import (
	"time"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

// MapCalendarEvent converts a CalendarEvent into a sekia Event, determining
// the event type based on status and creation time relative to lastSyncTime.
func MapCalendarEvent(ev CalendarEvent, calendarID string, lastSyncTime time.Time) protocol.Event {
	eventType := determineCalendarEventType(ev, lastSyncTime)

	payload := map[string]any{
		"id":          ev.ID,
		"summary":     ev.Summary,
		"description": ev.Description,
		"location":    ev.Location,
		"start":       ev.Start.Format(time.RFC3339),
		"end":         ev.End.Format(time.RFC3339),
		"status":      ev.Status,
		"organizer":   ev.Organizer,
		"attendees":   ev.Attendees,
		"html_link":   ev.HTMLLink,
		"calendar_id": calendarID,
	}

	return protocol.NewEvent(eventType, "google", payload)
}

// MapUpcomingEvent creates an event for a calendar event that is about to start.
func MapUpcomingEvent(ev CalendarEvent, calendarID string, minutesUntil int) protocol.Event {
	payload := map[string]any{
		"id":            ev.ID,
		"summary":       ev.Summary,
		"start":         ev.Start.Format(time.RFC3339),
		"end":           ev.End.Format(time.RFC3339),
		"minutes_until": minutesUntil,
		"location":      ev.Location,
		"html_link":     ev.HTMLLink,
		"calendar_id":   calendarID,
	}

	return protocol.NewEvent("google.calendar.event.upcoming", "google", payload)
}

func determineCalendarEventType(ev CalendarEvent, lastSyncTime time.Time) string {
	if ev.Status == "cancelled" {
		return "google.calendar.event.deleted"
	}
	if !ev.Created.IsZero() && ev.Created.After(lastSyncTime) {
		return "google.calendar.event.created"
	}
	return "google.calendar.event.updated"
}
