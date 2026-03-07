package mail

import (
	"fmt"
	"log"
	"time"

	"github.com/emersion/go-smtp"
	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/store"
)

type SMTPServer struct {
	server  *smtp.Server
	cfg     config.SMTPConfig
	handler *InboundHandler
}

func NewSMTPServer(cfg config.SMTPConfig, execStore *store.ExecutionStore, emailStore *store.EmailStore, workerStore *store.WorkerStore) *SMTPServer {
	handler := NewInboundHandler(cfg, execStore, emailStore, workerStore)

	s := smtp.NewServer(handler)
	s.Addr = fmt.Sprintf(":%d", cfg.Port)
	s.Domain = cfg.Domain
	s.ReadTimeout = 30 * time.Second
	s.WriteTimeout = 30 * time.Second
	s.MaxMessageBytes = 10 * 1024 * 1024 // 10MB
	s.MaxRecipients = 50
	s.AllowInsecureAuth = true

	return &SMTPServer{
		server:  s,
		cfg:     cfg,
		handler: handler,
	}
}

func (s *SMTPServer) Start() error {
	log.Printf("SMTP server starting on %s (domain: %s)", s.server.Addr, s.server.Domain)
	return s.server.ListenAndServe()
}

func (s *SMTPServer) Close() error {
	return s.server.Close()
}
