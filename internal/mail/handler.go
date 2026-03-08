package mail

import (
	"bytes"
	"io"
	"log"
	"net/mail"

	"github.com/emersion/go-smtp"
	"github.com/robobee/core/internal/config"
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

	log.Printf("Received email from=%s to=%v subject=%s", s.from, s.to, subject)

	return nil
}

func (s *session) Reset() {
	s.from = ""
	s.to = nil
}

func (s *session) Logout() error {
	return nil
}

