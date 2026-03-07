package mail

import (
	"fmt"
	"log"
	"net/smtp"
	"strings"

	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/store"
)

type Sender struct {
	cfg        config.SMTPConfig
	emailStore *store.EmailStore
}

func NewSender(cfg config.SMTPConfig, emailStore *store.EmailStore) *Sender {
	return &Sender{cfg: cfg, emailStore: emailStore}
}

func (s *Sender) SendReport(execution model.WorkerExecution, workerEmail string, recipients []string, subject, body string) error {
	msgID := fmt.Sprintf("<%s@%s>", execution.SessionID, s.cfg.Domain)

	allRecipients := make([]string, len(recipients))
	copy(allRecipients, recipients)

	// Add system CC
	ccAddr := ""
	if s.cfg.SystemCC != "" {
		allRecipients = append(allRecipients, s.cfg.SystemCC)
		ccAddr = s.cfg.SystemCC
	}

	msg := fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nCc: %s\r\nSubject: %s\r\nMessage-ID: %s\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		workerEmail,
		strings.Join(recipients, ", "),
		ccAddr,
		subject,
		msgID,
		body,
	)

	addr := fmt.Sprintf("localhost:%d", s.cfg.Port)
	err := smtp.SendMail(addr, nil, workerEmail, allRecipients, []byte(msg))
	if err != nil {
		log.Printf("failed to send email: %v", err)
		// Still store the email record even if send fails
	}

	// Store outbound email record
	s.emailStore.Create(model.Email{
		ExecutionID: execution.ID,
		FromAddr:    workerEmail,
		ToAddr:      strings.Join(recipients, ", "),
		CCAddr:      ccAddr,
		Subject:     subject,
		Body:        body,
		InReplyTo:   "",
		Direction:   model.EmailOutbound,
	})

	return err
}

func (s *Sender) SendApprovalRequest(execution model.WorkerExecution, workerEmail string, recipients []string, subject, body string) error {
	approvalBody := body + "\n\n---\nReply with 'approve/通过' to approve, or 'reject/驳回' with feedback to reject.\n"
	return s.SendReport(execution, workerEmail, recipients, "[Approval Required] "+subject, approvalBody)
}
