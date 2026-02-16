package slack

import (
	"strings"

	slackapi "github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"

	"github.com/sekia-ai/sekia/pkg/protocol"
)

// MapSlackEvent converts a Slack Events API inner event to a sekia Event.
// Returns the event and true, or zero value and false if unsupported.
// botUserID is used to filter self-messages and detect mentions.
func MapSlackEvent(innerEvent slackevents.EventsAPIInnerEvent, botUserID string) (protocol.Event, bool) {
	switch ev := innerEvent.Data.(type) {
	case *slackevents.MessageEvent:
		return mapMessageEvent(ev, botUserID)
	case *slackevents.ReactionAddedEvent:
		return mapReactionEvent(ev), true
	case *slackevents.ChannelCreatedEvent:
		return mapChannelCreatedEvent(ev), true
	default:
		return protocol.Event{}, false
	}
}

func mapMessageEvent(ev *slackevents.MessageEvent, botUserID string) (protocol.Event, bool) {
	// Skip bot's own messages and subtypes (edits, deletes, etc.).
	if ev.User == botUserID || ev.SubType != "" {
		return protocol.Event{}, false
	}

	var sekiaType string
	if containsMention(ev.Text, botUserID) {
		sekiaType = "slack.mention"
	} else {
		sekiaType = "slack.message.received"
	}

	payload := map[string]any{
		"channel":   ev.Channel,
		"user":      ev.User,
		"text":      ev.Text,
		"timestamp": ev.TimeStamp,
	}
	if ev.ThreadTimeStamp != "" {
		payload["thread_ts"] = ev.ThreadTimeStamp
	}

	return protocol.NewEvent(sekiaType, "slack", payload), true
}

func mapReactionEvent(ev *slackevents.ReactionAddedEvent) protocol.Event {
	payload := map[string]any{
		"user":      ev.User,
		"reaction":  ev.Reaction,
		"channel":   ev.Item.Channel,
		"timestamp": ev.Item.Timestamp,
	}
	return protocol.NewEvent("slack.reaction.added", "slack", payload)
}

func mapChannelCreatedEvent(ev *slackevents.ChannelCreatedEvent) protocol.Event {
	payload := map[string]any{
		"channel_id":   ev.Channel.ID,
		"channel_name": ev.Channel.Name,
		"creator":      ev.Channel.Creator,
	}
	return protocol.NewEvent("slack.channel.created", "slack", payload)
}

// containsMention checks if the text contains an @mention of the given user ID.
// Slack formats mentions as <@U12345>.
func containsMention(text, userID string) bool {
	return strings.Contains(text, "<@"+userID+">")
}

// MapInteractionCallback converts a Slack InteractionCallback to sekia Events.
// For block_actions, each action in the callback produces a separate event.
func MapInteractionCallback(callback slackapi.InteractionCallback) []protocol.Event {
	if callback.Type != slackapi.InteractionTypeBlockActions {
		return nil
	}

	var events []protocol.Event
	for _, action := range callback.ActionCallback.BlockActions {
		if action == nil {
			continue
		}

		payload := map[string]any{
			"action_id":   action.ActionID,
			"value":       action.Value,
			"block_id":    action.BlockID,
			"action_type": string(action.Type),
			"user":        callback.User.ID,
			"user_name":   callback.User.Name,
			"channel":     callback.Channel.ID,
			"message_ts":  callback.Container.MessageTs,
			"trigger_id":  callback.TriggerID,
		}

		eventType := "slack.action.button_clicked"
		if action.Type != slackapi.ActionType("button") {
			eventType = "slack.action." + string(action.Type)
		}

		events = append(events, protocol.NewEvent(eventType, "slack", payload))
	}

	return events
}
