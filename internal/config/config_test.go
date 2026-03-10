package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadMailConfig(t *testing.T) {
	content := `
mail:
  enabled: true
  imap_host: "imap.example.com:993"
  smtp_host: "smtp.example.com:587"
  username: "bot@example.com"
  password: "secret"
  poll_interval: "30s"
  mailbox: "INBOX"
`
	f, _ := os.CreateTemp("", "config-*.yaml")
	f.WriteString(content)
	f.Close()
	defer os.Remove(f.Name())

	cfg, err := Load(f.Name())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Mail.Enabled {
		t.Error("expected Mail.Enabled = true")
	}
	if cfg.Mail.IMAPHost != "imap.example.com:993" {
		t.Errorf("unexpected IMAPHost: %s", cfg.Mail.IMAPHost)
	}
	if cfg.Mail.SMTPHost != "smtp.example.com:587" {
		t.Errorf("unexpected SMTPHost: %s", cfg.Mail.SMTPHost)
	}
	if cfg.Mail.PollInterval != 30*time.Second {
		t.Errorf("unexpected PollInterval: %v", cfg.Mail.PollInterval)
	}
	if cfg.Mail.Mailbox != "INBOX" {
		t.Errorf("unexpected Mailbox: %s", cfg.Mail.Mailbox)
	}
}
