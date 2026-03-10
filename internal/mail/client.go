package mail

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	gomail "github.com/emersion/go-message/mail"
	"github.com/yuin/goldmark"

	"github.com/robobee/core/internal/config"
)

// EmailMessage represents a parsed inbound email.
type EmailMessage struct {
	MessageID  string
	InReplyTo  string
	References string
	From       string
	Subject    string
	Body       string  // plain-text body
	ThreadID   string  // resolved thread root Message-ID
}

// OutgoingEmail represents an email to send.
type OutgoingEmail struct {
	To         string
	Subject    string
	BodyMD     string // Markdown body (converted to HTML for sending)
	InReplyTo  string
	References string
}

// IMAPFetcher fetches unseen emails from the inbox.
type IMAPFetcher interface {
	FetchUnseen() ([]EmailMessage, error)
}

// EmailSender sends an email reply.
type EmailSender interface {
	Send(msg OutgoingEmail) error
}

// IMAPClient implements IMAPFetcher against a real IMAP server.
type IMAPClient struct {
	cfg config.MailConfig
}

func NewIMAPClient(cfg config.MailConfig) *IMAPClient {
	return &IMAPClient{cfg: cfg}
}

// FetchUnseen connects to IMAP, fetches all unseen messages, marks them seen, and disconnects.
func (c *IMAPClient) FetchUnseen() ([]EmailMessage, error) {
	cli, err := client.DialTLS(c.cfg.IMAPHost, &tls.Config{})
	if err != nil {
		return nil, fmt.Errorf("imap dial: %w", err)
	}
	defer cli.Logout()

	if err := cli.Login(c.cfg.Username, c.cfg.Password); err != nil {
		return nil, fmt.Errorf("imap login: %w", err)
	}

	if _, err := cli.Select(c.cfg.Mailbox, false); err != nil {
		return nil, fmt.Errorf("imap select %s: %w", c.cfg.Mailbox, err)
	}

	criteria := imap.NewSearchCriteria()
	criteria.WithoutFlags = []string{imap.SeenFlag}
	uids, err := cli.UidSearch(criteria)
	if err != nil {
		return nil, fmt.Errorf("imap search: %w", err)
	}
	if len(uids) == 0 {
		return nil, nil
	}

	seqset := new(imap.SeqSet)
	seqset.AddNum(uids...)

	section := &imap.BodySectionName{}
	items := []imap.FetchItem{imap.FetchEnvelope, section.FetchItem()}
	msgs := make(chan *imap.Message, len(uids))
	if err := cli.UidFetch(seqset, items, msgs); err != nil {
		return nil, fmt.Errorf("imap fetch: %w", err)
	}

	// Mark as seen
	flags := []interface{}{imap.SeenFlag}
	if err := cli.UidStore(seqset, imap.AddFlags, flags, nil); err != nil {
		log.Printf("mail: failed to mark messages as seen: %v", err)
	}

	var emails []EmailMessage
	for msg := range msgs {
		if msg == nil {
			continue
		}
		em, err := parseImapMessage(msg, section)
		if err != nil {
			log.Printf("mail: parse message error: %v", err)
			continue
		}
		emails = append(emails, em)
	}
	return emails, nil
}

func parseImapMessage(msg *imap.Message, section *imap.BodySectionName) (EmailMessage, error) {
	env := msg.Envelope
	em := EmailMessage{
		MessageID: env.MessageId,
		Subject:   env.Subject,
	}
	if len(env.From) > 0 {
		em.From = env.From[0].Address()
	}

	r := msg.GetBody(section)
	if r == nil {
		return em, nil
	}
	mr, err := gomail.CreateReader(r)
	if err != nil {
		return em, fmt.Errorf("create mail reader: %w", err)
	}

	em.InReplyTo = strings.TrimSpace(mr.Header.Get("In-Reply-To"))
	em.References = strings.TrimSpace(mr.Header.Get("References"))
	em.ThreadID = parseThreadID(em.MessageID, em.References)
	if em.InReplyTo != "" && em.ThreadID == "" {
		em.ThreadID = em.InReplyTo
	}
	if em.ThreadID == "" {
		em.ThreadID = em.MessageID
	}

	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		ct, _, _ := mime.ParseMediaType(p.Header.Get("Content-Type"))
		if ct == "text/plain" {
			body, _ := io.ReadAll(p.Body)
			em.Body = strings.TrimSpace(string(body))
			break
		}
	}
	return em, nil
}

// parseThreadID returns the root Message-ID for this email thread.
func parseThreadID(messageID, references string) string {
	references = strings.TrimSpace(references)
	if references == "" {
		return messageID
	}
	parts := strings.Fields(references)
	if len(parts) > 0 {
		return parts[0]
	}
	return messageID
}

// SMTPSender implements EmailSender using standard net/smtp with STARTTLS.
type SMTPSender struct {
	cfg config.MailConfig
}

func NewSMTPSender(cfg config.MailConfig) *SMTPSender {
	return &SMTPSender{cfg: cfg}
}

// Send sends an email reply with both plain-text and HTML parts.
func (s *SMTPSender) Send(msg OutgoingEmail) error {
	htmlBody := markdownToHTML(msg.BodyMD)

	var buf bytes.Buffer
	boundary := fmt.Sprintf("boundary_%d", time.Now().UnixNano())

	headers := textproto.MIMEHeader{}
	headers.Set("From", s.cfg.Username)
	headers.Set("To", msg.To)
	headers.Set("Subject", msg.Subject)
	headers.Set("MIME-Version", "1.0")
	headers.Set("Content-Type", fmt.Sprintf("multipart/alternative; boundary=%q", boundary))
	if msg.InReplyTo != "" {
		headers.Set("In-Reply-To", msg.InReplyTo)
	}
	if msg.References != "" {
		headers.Set("References", msg.References)
	}

	for k, vs := range headers {
		for _, v := range vs {
			fmt.Fprintf(&buf, "%s: %s\r\n", k, v)
		}
	}
	buf.WriteString("\r\n")

	mw := multipart.NewWriter(&buf)
	mw.SetBoundary(boundary)

	pw, _ := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/plain; charset=UTF-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	qw := quotedprintable.NewWriter(pw)
	qw.Write([]byte(msg.BodyMD))
	qw.Close()

	hw, _ := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/html; charset=UTF-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	qwh := quotedprintable.NewWriter(hw)
	qwh.Write([]byte(htmlBody))
	qwh.Close()

	mw.Close()

	host, _, _ := splitHost(s.cfg.SMTPHost)
	auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, host)
	return smtp.SendMail(s.cfg.SMTPHost, auth, s.cfg.Username, []string{msg.To}, buf.Bytes())
}

// markdownToHTML converts Markdown text to an HTML string.
func markdownToHTML(md string) string {
	var buf bytes.Buffer
	if err := goldmark.New().Convert([]byte(md), &buf); err != nil {
		return "<pre>" + md + "</pre>"
	}
	return buf.String()
}

// splitHost splits "host:port" into (host, port, error).
func splitHost(addr string) (string, string, error) {
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		return addr, "", nil
	}
	return addr[:idx], addr[idx+1:], nil
}
