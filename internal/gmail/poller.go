package gmail

import (
	"context"
	"time"

	"github.com/rs/zerolog"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

// Poller periodically checks for new emails via IMAP.
type Poller struct {
	client   GmailClient
	interval time.Duration
	folder   string
	onEvent  func(protocol.Event)
	logger   zerolog.Logger
	lastUID  uint32
}

// NewPoller creates an IMAP poller.
func NewPoller(client GmailClient, interval time.Duration, folder string, onEvent func(protocol.Event), logger zerolog.Logger) *Poller {
	return &Poller{
		client:   client,
		interval: interval,
		folder:   folder,
		onEvent:  onEvent,
		logger:   logger.With().Str("component", "imap-poller").Logger(),
		lastUID:  0,
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
	messages, highestUID, err := p.client.FetchNewMessages(ctx, p.folder, p.lastUID)
	if err != nil {
		p.logger.Error().Err(err).Msg("poll IMAP failed")
		return
	}

	for _, msg := range messages {
		ev := MapEmailEvent(msg)
		p.onEvent(ev)
		p.logger.Debug().
			Str("from", msg.From).
			Str("subject", msg.Subject).
			Msg("new email event")
	}

	if highestUID > p.lastUID {
		p.lastUID = highestUID
	}

	p.logger.Debug().
		Int("count", len(messages)).
		Uint32("last_uid", p.lastUID).
		Msg("poll complete")
}
