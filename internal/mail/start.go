package mail

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/robobee/core/internal/ai"
	"github.com/robobee/core/internal/botrouter"
	"github.com/robobee/core/internal/config"
	"github.com/robobee/core/internal/store"
	"github.com/robobee/core/internal/worker"
)

// Start begins the IMAP polling loop and blocks until ctx is cancelled.
// Call this in a goroutine from main.go.
func Start(
	ctx context.Context,
	cfg config.MailConfig,
	workerStore *store.WorkerStore,
	sessionStore *store.MailSessionStore,
	mgr *worker.Manager,
	aiClient *ai.Client,
) error {
	interval := cfg.PollInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}

	imapClient := NewIMAPClient(cfg)
	smtpSender := NewSMTPSender(cfg)
	router := botrouter.NewRouter(aiClient, workerStore)
	handler := NewHandler(router, sessionStore, mgr, smtpSender)

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
				if !subjectMatches(em.Subject, cfg.SubjectKeywords) {
					log.Printf("mail: skipping email subject=%q (no keyword match)", em.Subject)
					continue
				}
				handler.processEmail(em)
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
