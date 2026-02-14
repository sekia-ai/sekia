package google

import (
	"context"
	"math"
	"time"

	"github.com/rs/zerolog"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

// CalendarPoller periodically queries Google Calendar for changed events
// using the syncToken mechanism for incremental sync.
type CalendarPoller struct {
	client       CalendarClient
	interval     time.Duration
	calendarID   string
	upcomingMins int
	onEvent      func(protocol.Event)
	logger       zerolog.Logger
	syncToken    string
	lastSyncTime time.Time
	// Track upcoming events we've already notified about to avoid duplicates.
	notifiedUpcoming map[string]time.Time
}

// NewCalendarPoller creates a Google Calendar API poller.
func NewCalendarPoller(client CalendarClient, interval time.Duration, calendarID string, upcomingMins int, onEvent func(protocol.Event), logger zerolog.Logger) *CalendarPoller {
	return &CalendarPoller{
		client:           client,
		interval:         interval,
		calendarID:       calendarID,
		upcomingMins:     upcomingMins,
		onEvent:          onEvent,
		logger:           logger.With().Str("component", "calendar-poller").Logger(),
		lastSyncTime:     time.Now().Add(-24 * time.Hour),
		notifiedUpcoming: make(map[string]time.Time),
	}
}

// Run starts the polling loop. Blocks until ctx is cancelled.
func (p *CalendarPoller) Run(ctx context.Context) error {
	p.poll(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			p.poll(ctx)
		}
	}
}

func (p *CalendarPoller) poll(ctx context.Context) {
	p.pollChanges(ctx)

	if p.upcomingMins > 0 {
		p.pollUpcoming(ctx)
	}
}

func (p *CalendarPoller) pollChanges(ctx context.Context) {
	timeMin := time.Time{}
	if p.syncToken == "" {
		// First sync: look back 1 day.
		timeMin = time.Now().Add(-24 * time.Hour)
	}

	events, nextSyncToken, err := p.client.ListEvents(ctx, p.calendarID, p.syncToken, timeMin)
	if err != nil {
		p.logger.Error().Err(err).Msg("list calendar events failed")
		// On 410 Gone (syncToken expired), reset and reseed.
		p.syncToken = ""
		return
	}

	for _, ev := range events {
		sekiaEvent := MapCalendarEvent(ev, p.calendarID, p.lastSyncTime)
		p.onEvent(sekiaEvent)
		p.logger.Debug().
			Str("event_id", ev.ID).
			Str("summary", ev.Summary).
			Str("type", sekiaEvent.Type).
			Msg("calendar event")
	}

	if nextSyncToken != "" {
		p.syncToken = nextSyncToken
	}
	p.lastSyncTime = time.Now()

	p.logger.Debug().
		Int("count", len(events)).
		Msg("calendar poll complete")
}

func (p *CalendarPoller) pollUpcoming(ctx context.Context) {
	events, err := p.client.ListUpcomingEvents(ctx, p.calendarID, p.upcomingMins)
	if err != nil {
		p.logger.Error().Err(err).Msg("list upcoming events failed")
		return
	}

	now := time.Now()

	// Clean up past events from the notification map.
	for id, start := range p.notifiedUpcoming {
		if start.Before(now) {
			delete(p.notifiedUpcoming, id)
		}
	}

	for _, ev := range events {
		if _, notified := p.notifiedUpcoming[ev.ID]; notified {
			continue
		}

		minutesUntil := int(math.Ceil(time.Until(ev.Start).Minutes()))
		if minutesUntil < 0 {
			minutesUntil = 0
		}

		sekiaEvent := MapUpcomingEvent(ev, p.calendarID, minutesUntil)
		p.onEvent(sekiaEvent)
		p.notifiedUpcoming[ev.ID] = ev.Start

		p.logger.Debug().
			Str("event_id", ev.ID).
			Str("summary", ev.Summary).
			Int("minutes_until", minutesUntil).
			Msg("upcoming event notification")
	}
}
