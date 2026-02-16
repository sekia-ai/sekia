package slack

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
	slackapi "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

// SocketModeListener connects to Slack via Socket Mode and dispatches events.
type SocketModeListener struct {
	smClient  *socketmode.Client
	api       *slackapi.Client
	botUserID string
	onEvent   func(protocol.Event)
	logger    zerolog.Logger
}

// NewSocketModeListener creates a listener. Call Run() to start processing.
func NewSocketModeListener(botToken, appToken string, onEvent func(protocol.Event), logger zerolog.Logger) *SocketModeListener {
	api := slackapi.New(botToken, slackapi.OptionAppLevelToken(appToken))
	smClient := socketmode.New(api)

	return &SocketModeListener{
		smClient: smClient,
		api:      api,
		onEvent:  onEvent,
		logger:   logger.With().Str("component", "socketmode").Logger(),
	}
}

// Run starts the Socket Mode event loop. Blocks until ctx is cancelled.
func (sl *SocketModeListener) Run(ctx context.Context) error {
	// Resolve the bot's own user ID to filter self-messages.
	authResp, err := sl.api.AuthTestContext(ctx)
	if err != nil {
		return fmt.Errorf("auth test: %w", err)
	}
	sl.botUserID = authResp.UserID
	sl.logger.Info().Str("bot_user_id", sl.botUserID).Msg("authenticated")

	go sl.handleEvents(ctx)
	return sl.smClient.RunContext(ctx)
}

func (sl *SocketModeListener) handleEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-sl.smClient.Events:
			if !ok {
				return
			}
			sl.processEvent(evt)
		}
	}
}

func (sl *SocketModeListener) processEvent(evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeEventsAPI:
		eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			return
		}
		sl.smClient.Ack(*evt.Request)

		ev, mapped := MapSlackEvent(eventsAPIEvent.InnerEvent, sl.botUserID)
		if !mapped {
			sl.logger.Debug().
				Str("inner_type", eventsAPIEvent.InnerEvent.Type).
				Msg("ignoring unsupported slack event")
			return
		}

		sl.onEvent(ev)
		sl.logger.Info().Str("type", ev.Type).Msg("slack event dispatched")

	case socketmode.EventTypeInteractive:
		callback, ok := evt.Data.(slackapi.InteractionCallback)
		if !ok {
			return
		}
		sl.smClient.Ack(*evt.Request)

		events := MapInteractionCallback(callback)
		for _, ev := range events {
			sl.onEvent(ev)
			sl.logger.Info().Str("type", ev.Type).Msg("slack interactive event dispatched")
		}

	default:
		// Acknowledge non-EventsAPI types (slash commands, etc.).
		if evt.Request != nil {
			sl.smClient.Ack(*evt.Request)
		}
	}
}
