package google

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

// GmailPoller periodically queries Gmail for new messages using the History API.
type GmailPoller struct {
	client      GmailClient
	interval    time.Duration
	userID      string
	query       string
	maxMessages int64
	onEvent     func(protocol.Event)
	logger      zerolog.Logger
	historyID   uint64
	seeded      bool
}

// NewGmailPoller creates a Gmail API poller.
func NewGmailPoller(client GmailClient, interval time.Duration, userID, query string, maxMessages int64, onEvent func(protocol.Event), logger zerolog.Logger) *GmailPoller {
	if maxMessages <= 0 {
		maxMessages = 20
	}
	return &GmailPoller{
		client:      client,
		interval:    interval,
		userID:      userID,
		query:       query,
		maxMessages: maxMessages,
		onEvent:     onEvent,
		logger:      logger.With().Str("component", "gmail-poller").Logger(),
	}
}

// Run starts the polling loop. Blocks until ctx is cancelled.
func (p *GmailPoller) Run(ctx context.Context) error {
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

func (p *GmailPoller) poll(ctx context.Context) {
	if !p.seeded {
		p.seed(ctx)
		return
	}

	messages, newHistoryID, err := p.client.ListHistory(ctx, p.userID, p.historyID)
	if err != nil {
		p.logger.Error().Err(err).Uint64("history_id", p.historyID).Msg("list history failed")
		// On history ID too old (404), reseed.
		p.seeded = false
		return
	}

	for _, msg := range messages {
		ev := MapGmailEvent(msg)
		p.onEvent(ev)
		p.logger.Debug().
			Str("from", msg.From).
			Str("subject", msg.Subject).
			Msg("new email event")
	}

	if newHistoryID > p.historyID {
		p.historyID = newHistoryID
	}

	p.logger.Debug().
		Int("count", len(messages)).
		Uint64("history_id", p.historyID).
		Msg("gmail poll complete")
}

func (p *GmailPoller) seed(ctx context.Context) {
	_, historyID, err := p.client.GetProfile(ctx, p.userID)
	if err != nil {
		p.logger.Error().Err(err).Msg("get profile failed")
		return
	}

	p.historyID = historyID
	p.seeded = true

	// Fetch initial messages.
	messages, err := p.client.ListMessages(ctx, p.userID, p.query, p.maxMessages)
	if err != nil {
		p.logger.Error().Err(err).Msg("list initial messages failed")
		return
	}

	for _, msg := range messages {
		ev := MapGmailEvent(msg)
		p.onEvent(ev)
	}

	p.logger.Info().
		Uint64("history_id", p.historyID).
		Int("initial_messages", len(messages)).
		Msg("gmail poller seeded")
}
