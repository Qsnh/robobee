package mail

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/platform"
)

// MailPlatform implements platform.Platform for email.
type MailPlatform struct {
	receiver *MailReceiver
	sender   *MailSender
}

// NewPlatform constructs a MailPlatform from configuration.
func NewPlatform(cfg config.MailConfig) platform.Platform {
	return &MailPlatform{
		receiver: &MailReceiver{cfg: cfg},
		sender:   &MailSender{smtpSender: NewSMTPSender(cfg)},
	}
}

func (m *MailPlatform) ID() string                                { return "mail" }
func (m *MailPlatform) Receiver() platform.PlatformReceiverAdapter { return m.receiver }
func (m *MailPlatform) Sender() platform.PlatformSenderAdapter     { return m.sender }

// MailReceiver polls IMAP and dispatches inbound emails as platform messages.
type MailReceiver struct {
	cfg config.MailConfig
}

func (r *MailReceiver) Start(ctx context.Context, dispatch func(platform.InboundMessage)) error {
	interval := r.cfg.PollInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}

	imapClient := NewIMAPClient(r.cfg)

	log.Printf("Mail bot starting (poll interval: %s)...", interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			emails, err := imapClient.FetchUnseen()
			if err != nil {
				log.Printf("mail: fetch unseen error: %v", err)
				continue
			}
			if len(emails) > 0 {
				log.Printf("mail: fetched %d new email(s)", len(emails))
			}
			for _, em := range emails {
				if !subjectMatches(em.Subject, r.cfg.SubjectKeywords) {
					log.Printf("mail: skipping email subject=%q (no keyword match)", em.Subject)
					continue
				}
				log.Printf("mail: received email from=%s subject=%q thread=%s", em.From, em.Subject, em.ThreadID)
				em := em // capture loop variable
				dispatch(platform.InboundMessage{
					Platform:   "mail",
					SenderID:   em.From,
					SessionKey: "mail:" + em.ThreadID,
					Content:    em.Body,
					Raw:        em,
				})
			}
		}
	}
}

// subjectMatches returns true if keywords is empty or subject contains any keyword (case-insensitive).
func subjectMatches(subject string, keywords []string) bool {
	if len(keywords) == 0 {
		return true
	}
	lower := strings.ToLower(subject)
	for _, kw := range keywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// MailSender sends email replies via SMTP.
type MailSender struct {
	smtpSender EmailSender
}

func (s *MailSender) Send(ctx context.Context, msg platform.OutboundMessage) error {
	em, ok := msg.ReplyTo.Raw.(EmailMessage)
	if !ok {
		log.Printf("mail: sender: unexpected raw type %T", msg.ReplyTo.Raw)
		return nil
	}

	subject := em.Subject
	if subject != "" && !hasRePrefix(subject) {
		subject = "Re: " + subject
	}

	refs := em.References
	if em.MessageID != "" {
		if refs != "" {
			refs = refs + " " + em.MessageID
		} else {
			refs = em.MessageID
		}
	}

	if err := s.smtpSender.Send(OutgoingEmail{
		To:         em.From,
		Subject:    subject,
		BodyMD:     msg.Content,
		InReplyTo:  em.MessageID,
		References: refs,
	}); err != nil {
		log.Printf("mail: send reply error: %v", err)
		return err
	}
	log.Printf("mail: reply sent to=%s subject=%q", em.From, subject)
	return nil
}

func hasRePrefix(subject string) bool {
	return len(subject) >= 3 && (subject[:3] == "Re:" || subject[:3] == "re:")
}

var _ platform.Platform                = (*MailPlatform)(nil)
var _ platform.PlatformReceiverAdapter = (*MailReceiver)(nil)
var _ platform.PlatformSenderAdapter   = (*MailSender)(nil)
