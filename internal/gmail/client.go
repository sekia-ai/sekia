package gmail

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/smtp"
	"strings"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/mail"
)

// GmailClient abstracts the IMAP/SMTP operations used by the agent.
type GmailClient interface {
	// Polling (IMAP)
	FetchNewMessages(ctx context.Context, folder string, sinceUID uint32) ([]EmailMessage, uint32, error)

	// Commands
	SendEmail(ctx context.Context, to, subject, body string) error
	ReplyEmail(ctx context.Context, messageID, body string) error
	AddLabel(ctx context.Context, messageUID uint32, label string) error
	Archive(ctx context.Context, messageUID uint32) error
}

// EmailMessage is a minimal representation of an email.
type EmailMessage struct {
	UID       uint32
	MessageID string
	From      string
	To        string
	Subject   string
	Body      string
	Date      string
}

// realGmailClient implements GmailClient using go-imap and net/smtp.
type realGmailClient struct {
	imapServer string
	smtpServer string
	username   string
	password   string
}

func newRealGmailClient(cfg Config) *realGmailClient {
	return &realGmailClient{
		imapServer: cfg.IMAP.Server,
		smtpServer: cfg.SMTP.Server,
		username:   cfg.IMAP.Username,
		password:   cfg.IMAP.Password,
	}
}

func (c *realGmailClient) connectIMAP() (*imapclient.Client, error) {
	client, err := imapclient.DialTLS(c.imapServer, nil)
	if err != nil {
		return nil, fmt.Errorf("dial IMAP: %w", err)
	}
	if err := client.Login(c.username, c.password).Wait(); err != nil {
		client.Close()
		return nil, fmt.Errorf("login IMAP: %w", err)
	}
	return client, nil
}

func (c *realGmailClient) FetchNewMessages(_ context.Context, folder string, sinceUID uint32) ([]EmailMessage, uint32, error) {
	client, err := c.connectIMAP()
	if err != nil {
		return nil, sinceUID, err
	}
	defer client.Logout().Wait()

	if _, err := client.Select(folder, nil).Wait(); err != nil {
		return nil, sinceUID, fmt.Errorf("select %s: %w", folder, err)
	}

	// Search for unseen messages.
	criteria := &imap.SearchCriteria{
		NotFlag: []imap.Flag{imap.FlagSeen},
	}
	if sinceUID > 0 {
		uidSet := imap.UIDSet{}
		uidSet.AddRange(imap.UID(sinceUID+1), imap.UID(0)) // 0 means * (highest)
		criteria.UID = []imap.UIDSet{uidSet}
	}

	searchData, err := client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, sinceUID, fmt.Errorf("search: %w", err)
	}

	uidSet, ok := searchData.All.(imap.UIDSet)
	if !ok || len(uidSet) == 0 {
		return nil, sinceUID, nil
	}

	fetchOpts := &imap.FetchOptions{
		Envelope: true,
		UID:      true,
		Flags:    true,
		BodySection: []*imap.FetchItemBodySection{
			{Specifier: imap.PartSpecifierText, Peek: true},
		},
	}

	fetchCmd := client.Fetch(uidSet, fetchOpts)
	defer fetchCmd.Close()

	var messages []EmailMessage
	highestUID := sinceUID

	for {
		msg := fetchCmd.Next()
		if msg == nil {
			break
		}

		var em EmailMessage
		for {
			item := msg.Next()
			if item == nil {
				break
			}
			switch data := item.(type) {
			case imapclient.FetchItemDataUID:
				em.UID = uint32(data.UID)
			case imapclient.FetchItemDataEnvelope:
				env := data.Envelope
				em.Subject = env.Subject
				em.MessageID = env.MessageID
				em.Date = env.Date.String()
				if len(env.From) > 0 {
					em.From = env.From[0].Addr()
				}
				if len(env.To) > 0 {
					em.To = env.To[0].Addr()
				}
			case imapclient.FetchItemDataBodySection:
				bodyBytes, _ := io.ReadAll(data.Literal)
				em.Body = string(bodyBytes)
			}
		}

		if em.UID > 0 {
			messages = append(messages, em)
			if em.UID > highestUID {
				highestUID = em.UID
			}
		}
	}

	if err := fetchCmd.Close(); err != nil {
		return messages, highestUID, fmt.Errorf("fetch close: %w", err)
	}

	return messages, highestUID, nil
}

func (c *realGmailClient) SendEmail(_ context.Context, to, subject, body string) error {
	var buf bytes.Buffer
	var h mail.Header
	h.SetAddressList("From", []*mail.Address{{Address: c.username}})
	h.SetAddressList("To", []*mail.Address{{Address: to}})
	h.SetSubject(subject)

	w, err := mail.CreateSingleInlineWriter(&buf, h)
	if err != nil {
		return fmt.Errorf("create message: %w", err)
	}
	io.WriteString(w, body)
	w.Close()

	auth := smtp.PlainAuth("", c.username, c.password, strings.Split(c.smtpServer, ":")[0])
	return smtp.SendMail(c.smtpServer, auth, c.username, []string{to}, buf.Bytes())
}

func (c *realGmailClient) ReplyEmail(_ context.Context, messageID, body string) error {
	// Simplified reply: sends a new email referencing the original via In-Reply-To.
	// In a full implementation, we'd look up the original's From/Subject/To.
	var buf bytes.Buffer
	var h mail.Header
	h.SetAddressList("From", []*mail.Address{{Address: c.username}})
	h.Set("In-Reply-To", messageID)
	h.Set("References", messageID)

	w, err := mail.CreateSingleInlineWriter(&buf, h)
	if err != nil {
		return fmt.Errorf("create reply: %w", err)
	}
	io.WriteString(w, body)
	w.Close()

	auth := smtp.PlainAuth("", c.username, c.password, strings.Split(c.smtpServer, ":")[0])
	// Note: would need the original sender's address as the recipient.
	// This is a simplified implementation — full version would fetch the original.
	return smtp.SendMail(c.smtpServer, auth, c.username, []string{}, buf.Bytes())
}

func (c *realGmailClient) AddLabel(_ context.Context, messageUID uint32, label string) error {
	client, err := c.connectIMAP()
	if err != nil {
		return err
	}
	defer client.Logout().Wait()

	if _, err := client.Select("INBOX", nil).Wait(); err != nil {
		return fmt.Errorf("select INBOX: %w", err)
	}

	uidSet := imap.UIDSet{}
	uidSet.AddNum(imap.UID(messageUID))

	// Gmail maps labels to IMAP folders. Copy the message to the label folder.
	if _, err := client.Copy(uidSet, label).Wait(); err != nil {
		return fmt.Errorf("copy to %s: %w", label, err)
	}
	return nil
}

func (c *realGmailClient) Archive(_ context.Context, messageUID uint32) error {
	client, err := c.connectIMAP()
	if err != nil {
		return err
	}
	defer client.Logout().Wait()

	if _, err := client.Select("INBOX", nil).Wait(); err != nil {
		return fmt.Errorf("select INBOX: %w", err)
	}

	uidSet := imap.UIDSet{}
	uidSet.AddNum(imap.UID(messageUID))

	// Archive = move out of INBOX. Gmail's "All Mail" is the archive.
	if _, err := client.Move(uidSet, "[Gmail]/All Mail").Wait(); err != nil {
		return fmt.Errorf("move to archive: %w", err)
	}
	return nil
}
