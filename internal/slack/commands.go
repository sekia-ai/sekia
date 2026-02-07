package slack

import (
	"context"
	"fmt"

	slackapi "github.com/slack-go/slack"
)

// SlackClient abstracts the Slack API methods used by commands.
type SlackClient interface {
	PostMessage(ctx context.Context, channel, text string) error
	PostReply(ctx context.Context, channel, threadTS, text string) error
	AddReaction(ctx context.Context, channel, timestamp, emoji string) error
}

// realSlackClient wraps the slack-go/slack client.
type realSlackClient struct {
	client *slackapi.Client
}

func (c *realSlackClient) PostMessage(ctx context.Context, channel, text string) error {
	_, _, err := c.client.PostMessageContext(ctx, channel,
		slackapi.MsgOptionText(text, false))
	return err
}

func (c *realSlackClient) PostReply(ctx context.Context, channel, threadTS, text string) error {
	_, _, err := c.client.PostMessageContext(ctx, channel,
		slackapi.MsgOptionText(text, false),
		slackapi.MsgOptionTS(threadTS))
	return err
}

func (c *realSlackClient) AddReaction(ctx context.Context, channel, timestamp, emoji string) error {
	return c.client.AddReactionContext(ctx, emoji, slackapi.NewRefToMessage(channel, timestamp))
}

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

func cmdSendMessage(ctx context.Context, sc SlackClient, payload map[string]any) error {
	channel, err := extractString(payload, "channel")
	if err != nil {
		return err
	}
	text, err := extractString(payload, "text")
	if err != nil {
		return err
	}
	return sc.PostMessage(ctx, channel, text)
}

func cmdAddReaction(ctx context.Context, sc SlackClient, payload map[string]any) error {
	channel, err := extractString(payload, "channel")
	if err != nil {
		return err
	}
	timestamp, err := extractString(payload, "timestamp")
	if err != nil {
		return err
	}
	emoji, err := extractString(payload, "emoji")
	if err != nil {
		return err
	}
	return sc.AddReaction(ctx, channel, timestamp, emoji)
}

func cmdSendReply(ctx context.Context, sc SlackClient, payload map[string]any) error {
	channel, err := extractString(payload, "channel")
	if err != nil {
		return err
	}
	threadTS, err := extractString(payload, "thread_ts")
	if err != nil {
		return err
	}
	text, err := extractString(payload, "text")
	if err != nil {
		return err
	}
	return sc.PostReply(ctx, channel, threadTS, text)
}
