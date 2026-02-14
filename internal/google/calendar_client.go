package google

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

// CalendarEvent is a minimal representation of a Google Calendar event.
type CalendarEvent struct {
	ID          string
	Summary     string
	Description string
	Location    string
	Start       time.Time
	End         time.Time
	Status      string // "confirmed", "tentative", "cancelled"
	Organizer   string
	Attendees   []string
	HTMLLink    string
	Updated     time.Time
	Created     time.Time
}

// CalendarClient abstracts Google Calendar API operations.
type CalendarClient interface {
	// Polling
	ListEvents(ctx context.Context, calendarID string, syncToken string, timeMin time.Time) (events []CalendarEvent, nextSyncToken string, err error)
	ListUpcomingEvents(ctx context.Context, calendarID string, withinMins int) ([]CalendarEvent, error)

	// Commands
	CreateEvent(ctx context.Context, calendarID string, event CalendarEvent) (string, error)
	UpdateEvent(ctx context.Context, calendarID, eventID string, updates map[string]any) error
	DeleteEvent(ctx context.Context, calendarID, eventID string) error
}

// realCalendarClient implements CalendarClient using the Google Calendar REST API.
type realCalendarClient struct {
	svc *calendar.Service
}

func newRealCalendarClient(httpClient *http.Client) (*realCalendarClient, error) {
	svc, err := calendar.NewService(context.Background(), option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("create calendar service: %w", err)
	}
	return &realCalendarClient{svc: svc}, nil
}

func (c *realCalendarClient) ListEvents(ctx context.Context, calendarID string, syncToken string, timeMin time.Time) ([]CalendarEvent, string, error) {
	call := c.svc.Events.List(calendarID).
		SingleEvents(true).
		ShowDeleted(true).
		Context(ctx)

	if syncToken != "" {
		call = call.SyncToken(syncToken)
	} else {
		call = call.TimeMin(timeMin.Format(time.RFC3339))
	}

	var events []CalendarEvent
	var nextSyncToken string

	err := call.Pages(ctx, func(resp *calendar.Events) error {
		nextSyncToken = resp.NextSyncToken
		for _, item := range resp.Items {
			events = append(events, mapCalendarItem(item))
		}
		return nil
	})
	if err != nil {
		return nil, "", fmt.Errorf("list events: %w", err)
	}

	return events, nextSyncToken, nil
}

func (c *realCalendarClient) ListUpcomingEvents(ctx context.Context, calendarID string, withinMins int) ([]CalendarEvent, error) {
	now := time.Now()
	call := c.svc.Events.List(calendarID).
		SingleEvents(true).
		TimeMin(now.Format(time.RFC3339)).
		TimeMax(now.Add(time.Duration(withinMins) * time.Minute).Format(time.RFC3339)).
		OrderBy("startTime").
		Context(ctx)

	resp, err := call.Do()
	if err != nil {
		return nil, fmt.Errorf("list upcoming events: %w", err)
	}

	var events []CalendarEvent
	for _, item := range resp.Items {
		events = append(events, mapCalendarItem(item))
	}

	return events, nil
}

func (c *realCalendarClient) CreateEvent(ctx context.Context, calendarID string, event CalendarEvent) (string, error) {
	item := &calendar.Event{
		Summary:     event.Summary,
		Description: event.Description,
		Location:    event.Location,
		Start:       &calendar.EventDateTime{DateTime: event.Start.Format(time.RFC3339)},
		End:         &calendar.EventDateTime{DateTime: event.End.Format(time.RFC3339)},
	}

	for _, email := range event.Attendees {
		item.Attendees = append(item.Attendees, &calendar.EventAttendee{Email: email})
	}

	created, err := c.svc.Events.Insert(calendarID, item).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("create event: %w", err)
	}
	return created.Id, nil
}

func (c *realCalendarClient) UpdateEvent(ctx context.Context, calendarID, eventID string, updates map[string]any) error {
	item := &calendar.Event{}

	if v, ok := updates["summary"].(string); ok {
		item.Summary = v
	}
	if v, ok := updates["description"].(string); ok {
		item.Description = v
	}
	if v, ok := updates["location"].(string); ok {
		item.Location = v
	}
	if v, ok := updates["start"].(string); ok {
		item.Start = &calendar.EventDateTime{DateTime: v}
	}
	if v, ok := updates["end"].(string); ok {
		item.End = &calendar.EventDateTime{DateTime: v}
	}

	_, err := c.svc.Events.Patch(calendarID, eventID, item).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("update event: %w", err)
	}
	return nil
}

func (c *realCalendarClient) DeleteEvent(ctx context.Context, calendarID, eventID string) error {
	if err := c.svc.Events.Delete(calendarID, eventID).Context(ctx).Do(); err != nil {
		return fmt.Errorf("delete event: %w", err)
	}
	return nil
}

func mapCalendarItem(item *calendar.Event) CalendarEvent {
	ev := CalendarEvent{
		ID:          item.Id,
		Summary:     item.Summary,
		Description: item.Description,
		Location:    item.Location,
		Status:      item.Status,
		HTMLLink:    item.HtmlLink,
	}

	if item.Start != nil {
		ev.Start, _ = time.Parse(time.RFC3339, item.Start.DateTime)
		if ev.Start.IsZero() {
			ev.Start, _ = time.Parse("2006-01-02", item.Start.Date)
		}
	}
	if item.End != nil {
		ev.End, _ = time.Parse(time.RFC3339, item.End.DateTime)
		if ev.End.IsZero() {
			ev.End, _ = time.Parse("2006-01-02", item.End.Date)
		}
	}
	if item.Organizer != nil {
		ev.Organizer = item.Organizer.Email
	}
	for _, a := range item.Attendees {
		ev.Attendees = append(ev.Attendees, a.Email)
	}
	if item.Updated != "" {
		ev.Updated, _ = time.Parse(time.RFC3339, item.Updated)
	}
	if item.Created != "" {
		ev.Created, _ = time.Parse(time.RFC3339, item.Created)
	}

	return ev
}
