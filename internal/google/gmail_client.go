package google

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// EmailMessage is a minimal representation of an email from the Gmail API.
type EmailMessage struct {
	ID        string   // Gmail message ID
	ThreadID  string   // Gmail thread ID
	MessageID string   // RFC 2822 Message-ID header
	From      string
	To        string
	Subject   string
	Body      string
	Date      string
	Labels    []string
}

// GmailClient abstracts Gmail REST API operations.
type GmailClient interface {
	// Polling
	GetProfile(ctx context.Context, userID string) (emailAddress string, historyID uint64, err error)
	ListHistory(ctx context.Context, userID string, startHistoryID uint64) ([]EmailMessage, uint64, error)
	ListMessages(ctx context.Context, userID string, query string, maxResults int64) ([]EmailMessage, error)

	// Commands
	SendEmail(ctx context.Context, userID, to, subject, body string) error
	ReplyEmail(ctx context.Context, userID, threadID, inReplyTo, to, subject, body string) error
	AddLabel(ctx context.Context, userID, messageID, labelName string) error
	RemoveLabel(ctx context.Context, userID, messageID, labelName string) error
	Archive(ctx context.Context, userID, messageID string) error
	Trash(ctx context.Context, userID, messageID string) error
	Delete(ctx context.Context, userID, messageID string) error
}

// realGmailClient implements GmailClient using the Gmail REST API.
type realGmailClient struct {
	svc *gmail.Service
}

func newRealGmailClient(httpClient *http.Client) (*realGmailClient, error) {
	svc, err := gmail.NewService(context.Background(), option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("create gmail service: %w", err)
	}
	return &realGmailClient{svc: svc}, nil
}

func (c *realGmailClient) GetProfile(ctx context.Context, userID string) (string, uint64, error) {
	profile, err := c.svc.Users.GetProfile(userID).Context(ctx).Do()
	if err != nil {
		return "", 0, fmt.Errorf("get profile: %w", err)
	}
	return profile.EmailAddress, uint64(profile.HistoryId), nil
}

func (c *realGmailClient) ListHistory(ctx context.Context, userID string, startHistoryID uint64) ([]EmailMessage, uint64, error) {
	call := c.svc.Users.History.List(userID).
		StartHistoryId(startHistoryID).
		HistoryTypes("messageAdded").
		Context(ctx)

	var messages []EmailMessage
	var highestHistoryID uint64

	err := call.Pages(ctx, func(resp *gmail.ListHistoryResponse) error {
		highestHistoryID = uint64(resp.HistoryId)
		for _, h := range resp.History {
			for _, added := range h.MessagesAdded {
				msg, err := c.getMessage(ctx, userID, added.Message.Id)
				if err != nil {
					continue // skip messages we can't fetch
				}
				messages = append(messages, msg)
			}
		}
		return nil
	})
	if err != nil {
		return nil, startHistoryID, fmt.Errorf("list history: %w", err)
	}

	if highestHistoryID == 0 {
		highestHistoryID = startHistoryID
	}

	return messages, highestHistoryID, nil
}

func (c *realGmailClient) ListMessages(ctx context.Context, userID string, query string, maxResults int64) ([]EmailMessage, error) {
	call := c.svc.Users.Messages.List(userID).Context(ctx).MaxResults(maxResults)
	if query != "" {
		call = call.Q(query)
	}

	resp, err := call.Do()
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}

	var messages []EmailMessage
	for _, m := range resp.Messages {
		msg, err := c.getMessage(ctx, userID, m.Id)
		if err != nil {
			continue
		}
		messages = append(messages, msg)
	}

	return messages, nil
}

func (c *realGmailClient) getMessage(ctx context.Context, userID, messageID string) (EmailMessage, error) {
	msg, err := c.svc.Users.Messages.Get(userID, messageID).
		Format("full").Context(ctx).Do()
	if err != nil {
		return EmailMessage{}, fmt.Errorf("get message %s: %w", messageID, err)
	}

	em := EmailMessage{
		ID:       msg.Id,
		ThreadID: msg.ThreadId,
		Labels:   msg.LabelIds,
	}

	for _, header := range msg.Payload.Headers {
		switch strings.ToLower(header.Name) {
		case "from":
			em.From = header.Value
		case "to":
			em.To = header.Value
		case "subject":
			em.Subject = header.Value
		case "date":
			em.Date = header.Value
		case "message-id":
			em.MessageID = header.Value
		}
	}

	em.Body = extractPlainTextBody(msg.Payload)

	return em, nil
}

func extractPlainTextBody(payload *gmail.MessagePart) string {
	if payload.MimeType == "text/plain" && payload.Body != nil && payload.Body.Data != "" {
		data, err := base64.URLEncoding.DecodeString(payload.Body.Data)
		if err == nil {
			return string(data)
		}
	}

	for _, part := range payload.Parts {
		if text := extractPlainTextBody(part); text != "" {
			return text
		}
	}

	// Fallback: use snippet if no plain text part found.
	return ""
}

func (c *realGmailClient) SendEmail(ctx context.Context, userID, to, subject, body string) error {
	raw := buildRFC2822("", to, subject, body, "", "")
	msg := &gmail.Message{Raw: base64.URLEncoding.EncodeToString([]byte(raw))}
	_, err := c.svc.Users.Messages.Send(userID, msg).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("send email: %w", err)
	}
	return nil
}

func (c *realGmailClient) ReplyEmail(ctx context.Context, userID, threadID, inReplyTo, to, subject, body string) error {
	raw := buildRFC2822("", to, subject, body, inReplyTo, inReplyTo)
	msg := &gmail.Message{
		Raw:      base64.URLEncoding.EncodeToString([]byte(raw)),
		ThreadId: threadID,
	}
	_, err := c.svc.Users.Messages.Send(userID, msg).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("reply email: %w", err)
	}
	return nil
}

func (c *realGmailClient) AddLabel(ctx context.Context, userID, messageID, labelName string) error {
	mod := &gmail.ModifyMessageRequest{AddLabelIds: []string{labelName}}
	_, err := c.svc.Users.Messages.Modify(userID, messageID, mod).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("add label: %w", err)
	}
	return nil
}

func (c *realGmailClient) RemoveLabel(ctx context.Context, userID, messageID, labelName string) error {
	mod := &gmail.ModifyMessageRequest{RemoveLabelIds: []string{labelName}}
	_, err := c.svc.Users.Messages.Modify(userID, messageID, mod).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("remove label: %w", err)
	}
	return nil
}

func (c *realGmailClient) Archive(ctx context.Context, userID, messageID string) error {
	mod := &gmail.ModifyMessageRequest{RemoveLabelIds: []string{"INBOX"}}
	_, err := c.svc.Users.Messages.Modify(userID, messageID, mod).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("archive: %w", err)
	}
	return nil
}

func (c *realGmailClient) Trash(ctx context.Context, userID, messageID string) error {
	_, err := c.svc.Users.Messages.Trash(userID, messageID).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("trash: %w", err)
	}
	return nil
}

func (c *realGmailClient) Delete(ctx context.Context, userID, messageID string) error {
	err := c.svc.Users.Messages.Delete(userID, messageID).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

// buildRFC2822 constructs a minimal RFC 2822 email message.
func buildRFC2822(from, to, subject, body, inReplyTo, references string) string {
	var b strings.Builder
	if from != "" {
		fmt.Fprintf(&b, "From: %s\r\n", from)
	}
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	if inReplyTo != "" {
		fmt.Fprintf(&b, "In-Reply-To: %s\r\n", inReplyTo)
		fmt.Fprintf(&b, "References: %s\r\n", references)
	}
	fmt.Fprintf(&b, "Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	fmt.Fprintf(&b, "\r\n%s", body)
	return b.String()
}
