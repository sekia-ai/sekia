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
	smClient := socketmode.New(api, socketmode.OptionDebug(true))

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
	defer func() {
		if r := recover(); r != nil {
			sl.logger.Error().Interface("panic", r).Msg("handleEvents goroutine panicked — interactive events will no longer be acknowledged")
		}
	}()

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
	sl.logger.Debug().Str("event_type", string(evt.Type)).Msg("socket mode event received")

	switch evt.Type {
	case socketmode.EventTypeEventsAPI:
		sl.smClient.Ack(*evt.Request)

		eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
		if !ok {
			sl.logger.Warn().Msg("unexpected data type for events API event")
			return
		}

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
		sl.logger.Info().Msg("interactive event received, sending ack")

		// Always acknowledge interactive events immediately to prevent
		// Slack from showing a warning triangle to the user.
		sl.smClient.Ack(*evt.Request)

		callback, ok := evt.Data.(slackapi.InteractionCallback)
		if !ok {
			sl.logger.Warn().Msg("unexpected data type for interactive event")
			return
		}

		sl.logger.Debug().
			Str("callback_type", string(callback.Type)).
			Int("actions", len(callback.ActionCallback.BlockActions)).
			Msg("interactive callback parsed")

		events := MapInteractionCallback(callback)
		if len(events) == 0 {
			sl.logger.Warn().
				Str("callback_type", string(callback.Type)).
				Msg("interactive callback produced no events")
		}
		for _, ev := range events {
			sl.onEvent(ev)
			sl.logger.Info().Str("type", ev.Type).Msg("slack interactive event dispatched")
		}

	default:
		// Acknowledge non-EventsAPI types (slash commands, etc.)
		// but skip internal events (hello, connecting, connected) that
		// have no envelope ID — sending an empty ack kills the connection.
		if evt.Request != nil && evt.Request.EnvelopeID != "" {
			sl.smClient.Ack(*evt.Request)
		}
	}
}
