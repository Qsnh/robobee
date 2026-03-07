package mail

import (
	"bytes"
	"io"
	"log"
	"net/mail"
	"strings"

	"github.com/emersion/go-smtp"
	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/store"
)

type InboundHandler struct {
	cfg         config.SMTPConfig
	execStore   *store.ExecutionStore
	emailStore  *store.EmailStore
	workerStore *store.WorkerStore
}

func NewInboundHandler(cfg config.SMTPConfig, es *store.ExecutionStore, emailS *store.EmailStore, ws *store.WorkerStore) *InboundHandler {
	return &InboundHandler{
		cfg:         cfg,
		execStore:   es,
		emailStore:  emailS,
		workerStore: ws,
	}
}

// Backend interface methods
func (h *InboundHandler) NewSession(_ *smtp.Conn) (smtp.Session, error) {
	return &session{handler: h}, nil
}

type session struct {
	handler *InboundHandler
	from    string
	to      []string
}

func (s *session) AuthPlain(username, password string) error {
	return nil // Accept all auth for internal use
}

func (s *session) Mail(from string, opts *smtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *session) Rcpt(to string, opts *smtp.RcptOptions) error {
	s.to = append(s.to, to)
	return nil
}

func (s *session) Data(r io.Reader) error {
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		return err
	}

	msg, err := mail.ReadMessage(&buf)
	if err != nil {
		log.Printf("failed to parse email: %v", err)
		return err
	}

	subject := msg.Header.Get("Subject")
	inReplyTo := msg.Header.Get("In-Reply-To")
	body, _ := io.ReadAll(msg.Body)

	log.Printf("Received email from=%s to=%v subject=%s in_reply_to=%s", s.from, s.to, subject, inReplyTo)

	// Handle approval reply
	if inReplyTo != "" {
		s.handler.handleApprovalReply(s.from, inReplyTo, subject, string(body))
	}

	return nil
}

func (s *session) Reset() {
	s.from = ""
	s.to = nil
}

func (s *session) Logout() error {
	return nil
}

func (h *InboundHandler) handleApprovalReply(from, inReplyTo, subject, body string) {
	// inReplyTo format: <session_id@domain>
	sessionID := strings.TrimPrefix(inReplyTo, "<")
	sessionID = strings.Split(sessionID, "@")[0]

	exec, err := h.execStore.GetBySessionID(sessionID)
	if err != nil {
		log.Printf("no execution found for session %s: %v", sessionID, err)
		return
	}

	if exec.Status != model.ExecStatusAwaitingApproval {
		log.Printf("execution %s not awaiting approval (status=%s)", exec.ID, exec.Status)
		return
	}

	// Store the reply email
	h.emailStore.Create(model.Email{
		ExecutionID: exec.ID,
		FromAddr:    from,
		ToAddr:      "",
		Subject:     subject,
		Body:        body,
		InReplyTo:   inReplyTo,
		Direction:   model.EmailInbound,
	})

	// Parse approval/rejection from body
	lowerBody := strings.ToLower(body)
	if strings.Contains(lowerBody, "approve") || strings.Contains(lowerBody, "通过") {
		log.Printf("Execution %s approved via email", exec.ID)
		h.execStore.UpdateStatus(exec.ID, model.ExecStatusApproved)
	} else if strings.Contains(lowerBody, "reject") || strings.Contains(lowerBody, "驳回") {
		log.Printf("Execution %s rejected via email", exec.ID)
		h.execStore.UpdateStatus(exec.ID, model.ExecStatusRejected)
	} else {
		log.Printf("Unrecognized reply for execution %s, treating as feedback", exec.ID)
	}
}
