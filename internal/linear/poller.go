package linear

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

// Poller periodically queries Linear for updated issues and comments.
type Poller struct {
	client       LinearClient
	interval     time.Duration
	teamFilter   string
	onEvent      func(protocol.Event)
	logger       zerolog.Logger
	lastSyncTime time.Time
}

// NewPoller creates a Linear API poller.
func NewPoller(client LinearClient, interval time.Duration, teamFilter string, onEvent func(protocol.Event), logger zerolog.Logger) *Poller {
	return &Poller{
		client:       client,
		interval:     interval,
		teamFilter:   teamFilter,
		onEvent:      onEvent,
		logger:       logger.With().Str("component", "poller").Logger(),
		lastSyncTime: time.Now().Add(-5 * time.Minute),
	}
}

// Run starts the polling loop. Blocks until ctx is cancelled.
func (p *Poller) Run(ctx context.Context) error {
	// Do an initial poll immediately.
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

func (p *Poller) poll(ctx context.Context) {
	since := p.lastSyncTime
	now := time.Now()

	issues, err := p.client.FetchUpdatedIssues(ctx, since, p.teamFilter)
	if err != nil {
		p.logger.Error().Err(err).Msg("poll issues failed")
		return
	}

	for _, issue := range issues {
		ev := MapIssueEvent(issue, since)
		p.onEvent(ev)
		p.logger.Debug().
			Str("type", ev.Type).
			Str("identifier", issue.Identifier).
			Msg("issue event")
	}

	comments, err := p.client.FetchUpdatedComments(ctx, since)
	if err != nil {
		p.logger.Error().Err(err).Msg("poll comments failed")
	} else {
		for _, comment := range comments {
			ev := MapCommentEvent(comment)
			p.onEvent(ev)
		}
	}

	p.lastSyncTime = now
	p.logger.Debug().
		Int("issues", len(issues)).
		Int("comments", len(comments)).
		Msg("poll complete")
}
