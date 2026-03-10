# Mail-Based Task Scheduling Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add email-based task scheduling via IMAP polling + SMTP replies, following the same patterns as the existing Feishu and DingTalk integrations.

**Architecture:** A configurable-interval IMAP poller fetches unseen emails, routes each message to a worker via `botrouter`, executes the worker, then replies with the Markdown-rendered-as-HTML result via SMTP. Sessions are tracked per email thread using `Message-ID` / `In-Reply-To` / `References` headers.

**Tech Stack:** `github.com/emersion/go-imap` (IMAP), `github.com/emersion/go-message` (MIME parse), standard `net/smtp` (send), `github.com/yuin/goldmark` (Markdown→HTML), SQLite (sessions)

---

## Chunk 1: Dependencies and Configuration

### Task 1: Add Go dependencies

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Add dependencies via go get**

```bash
cd /path/to/project
go get github.com/emersion/go-imap@latest
go get github.com/emersion/go-message@latest
go get github.com/yuin/goldmark@latest
go mod tidy
```

Expected: `go.mod` and `go.sum` updated with three new direct dependencies.

- [ ] **Step 2: Verify go.mod updated**

```bash
grep -E "go-imap|go-message|goldmark" go.mod
```

Expected output contains three lines like:
```
github.com/emersion/go-imap v1.x.x
github.com/emersion/go-message v0.x.x
github.com/yuin/goldmark v1.x.x
```

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add go-imap, go-message, goldmark dependencies"
```

---

### Task 2: Add MailConfig struct and update config.example.yaml

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.example.yaml`

- [ ] **Step 1: Write the failing test**

Create `internal/config/config_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/config/... -v -run TestLoadMailConfig
```

Expected: FAIL — `cfg.Mail` field does not exist.

- [ ] **Step 3: Add MailConfig to config.go**

In `internal/config/config.go`, add `Mail MailConfig` to the `Config` struct and add the new struct:

```go
// In Config struct, add after DingTalk field:
Mail     MailConfig     `yaml:"mail"`
```

```go
type MailConfig struct {
	Enabled      bool          `yaml:"enabled"`
	IMAPHost     string        `yaml:"imap_host"`
	SMTPHost     string        `yaml:"smtp_host"`
	Username     string        `yaml:"username"`
	Password     string        `yaml:"password"`
	PollInterval time.Duration `yaml:"poll_interval"`
	Mailbox      string        `yaml:"mailbox"`
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/config/... -v -run TestLoadMailConfig
```

Expected: PASS

- [ ] **Step 5: Update config.example.yaml**

Append to the end of `config.example.yaml`:

```yaml

mail:
  enabled: false
  imap_host: "imap.gmail.com:993"
  smtp_host: "smtp.gmail.com:587"
  username: ""
  password: ""
  poll_interval: "30s"
  mailbox: "INBOX"
```

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go config.example.yaml
git commit -m "feat: add MailConfig struct and example config"
```

---

## Chunk 2: Database Migration and Session Store

### Task 3: Add mail_sessions table migration

**Files:**
- Modify: `internal/store/db.go`

- [ ] **Step 1: Add migration to db.go**

In `internal/store/db.go`, add a new entry to the `migrations` slice after the `dingtalk_sessions` entry:

```go
`CREATE TABLE IF NOT EXISTS mail_sessions (
    thread_id           TEXT NOT NULL,
    worker_id           TEXT NOT NULL,
    session_id          TEXT NOT NULL,
    last_execution_id   TEXT NOT NULL DEFAULT '',
    updated_at          DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (thread_id)
)`,
```

> **Note:** This schema intentionally adds `worker_id` and `last_execution_id` beyond the spec's table definition. These are required to support multi-turn conversations (same as the Feishu/DingTalk session tables), and the spec omitted them by oversight.

- [ ] **Step 2: Verify migration compiles**

```bash
go build ./internal/store/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/store/db.go
git commit -m "feat: add mail_sessions table migration"
```

---

### Task 4: Create mail session store

**Files:**
- Create: `internal/store/mail_session_store.go`
- Create: `internal/store/mail_session_store_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/mail_session_store_test.go`:

```go
package store

import (
	"testing"
)

func TestMailSessionStore(t *testing.T) {
	db, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	defer db.Close()

	s := NewMailSessionStore(db)

	// GetSession on empty store returns nil
	sess, err := s.GetSession("thread-1")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess != nil {
		t.Errorf("expected nil session, got %+v", sess)
	}

	// Upsert creates a session
	if err := s.UpsertSession("thread-1", "worker-a", "claude-sess-1", "exec-1"); err != nil {
		t.Fatalf("UpsertSession: %v", err)
	}

	sess, err = s.GetSession("thread-1")
	if err != nil {
		t.Fatalf("GetSession after upsert: %v", err)
	}
	if sess == nil {
		t.Fatal("expected session, got nil")
	}
	if sess.ThreadID != "thread-1" || sess.WorkerID != "worker-a" || sess.SessionID != "claude-sess-1" || sess.LastExecutionID != "exec-1" {
		t.Errorf("unexpected session: %+v", sess)
	}

	// Upsert updates existing session
	if err := s.UpsertSession("thread-1", "worker-b", "claude-sess-2", "exec-2"); err != nil {
		t.Fatalf("UpsertSession update: %v", err)
	}
	sess, _ = s.GetSession("thread-1")
	if sess.WorkerID != "worker-b" || sess.LastExecutionID != "exec-2" {
		t.Errorf("expected updated session, got %+v", sess)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/store/... -v -run TestMailSessionStore
```

Expected: FAIL — `NewMailSessionStore` not defined.

- [ ] **Step 3: Implement mail_session_store.go**

Create `internal/store/mail_session_store.go`:

```go
package store

import (
	"database/sql"
	"time"
)

type MailSession struct {
	ThreadID        string
	WorkerID        string
	SessionID       string
	LastExecutionID string
	UpdatedAt       time.Time
}

// GetWorkerID and GetLastExecutionID implement the threadSession interface
// used by the mail handler. Defined here (same package) to avoid
// the Go restriction against defining methods on non-local types.
func (s *MailSession) GetWorkerID() string        { return s.WorkerID }
func (s *MailSession) GetLastExecutionID() string { return s.LastExecutionID }

type MailSessionStore struct {
	db *sql.DB
}

func NewMailSessionStore(db *sql.DB) *MailSessionStore {
	return &MailSessionStore{db: db}
}

// GetSession returns nil if not found.
func (s *MailSessionStore) GetSession(threadID string) (*MailSession, error) {
	row := s.db.QueryRow(
		`SELECT thread_id, worker_id, session_id, last_execution_id, updated_at
         FROM mail_sessions WHERE thread_id = ?`, threadID)

	var sess MailSession
	err := row.Scan(&sess.ThreadID, &sess.WorkerID, &sess.SessionID, &sess.LastExecutionID, &sess.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sess, nil
}

// UpsertSession creates or updates the session mapping for a mail thread.
func (s *MailSessionStore) UpsertSession(threadID, workerID, sessionID, lastExecutionID string) error {
	_, err := s.db.Exec(
		`INSERT INTO mail_sessions (thread_id, worker_id, session_id, last_execution_id, updated_at)
         VALUES (?, ?, ?, ?, ?)
         ON CONFLICT(thread_id) DO UPDATE SET
             worker_id = excluded.worker_id,
             session_id = excluded.session_id,
             last_execution_id = excluded.last_execution_id,
             updated_at = excluded.updated_at`,
		threadID, workerID, sessionID, lastExecutionID, time.Now().UTC())
	return err
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./internal/store/... -v -run TestMailSessionStore
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/mail_session_store.go internal/store/mail_session_store_test.go
git commit -m "feat: add MailSessionStore with thread-based session tracking"
```

---

## Chunk 3: Mail Package — Client

### Task 5: Create mail/client.go with IMAP and SMTP logic

**Files:**
- Create: `internal/mail/client.go`
- Create: `internal/mail/client_test.go`

The client exposes two interfaces (`IMAPFetcher`, `EmailSender`) for testability, plus concrete `IMAPClient` and `SMTPSender` implementations.

- [ ] **Step 1: Write the failing tests**

Create `internal/mail/client_test.go`:

```go
package mail

import (
	"strings"
	"testing"
)

func TestParseThreadID_NoReferences(t *testing.T) {
	// A fresh email (no In-Reply-To) uses its own Message-ID as thread_id
	id := parseThreadID("", "")
	if id != "" {
		t.Errorf("expected empty string for no message-id, got %q", id)
	}

	id = parseThreadID("<msg-001@example.com>", "")
	if id != "<msg-001@example.com>" {
		t.Errorf("unexpected thread id: %q", id)
	}
}

func TestParseThreadID_WithReferences(t *testing.T) {
	// The root of a thread is the first entry in References
	refs := "<root@example.com> <mid@example.com> <leaf@example.com>"
	id := parseThreadID("<leaf@example.com>", refs)
	if id != "<root@example.com>" {
		t.Errorf("expected root message-id, got %q", id)
	}
}

func TestParseThreadID_SingleReference(t *testing.T) {
	id := parseThreadID("<reply@example.com>", "<root@example.com>")
	if id != "<root@example.com>" {
		t.Errorf("expected root message-id, got %q", id)
	}
}

func TestMarkdownToHTML(t *testing.T) {
	md := "**Hello** _world_"
	html := markdownToHTML(md)
	if !strings.Contains(html, "<strong>Hello</strong>") {
		t.Errorf("expected bold tag, got: %s", html)
	}
	if !strings.Contains(html, "<em>world</em>") {
		t.Errorf("expected em tag, got: %s", html)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/mail/... -v -run "TestParseThreadID|TestMarkdownToHTML"
```

Expected: FAIL — package `mail` does not exist.

- [ ] **Step 3: Implement client.go**

Create `internal/mail/client.go`:

```go
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
	Body       string   // plain-text body
	ThreadID   string   // resolved thread root Message-ID
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
		MessageID:  env.MessageId,
		Subject:    env.Subject,
	}
	if len(env.From) > 0 {
		em.From = env.From[0].Address()
	}

	// Parse body using go-message
	r := msg.GetBody(section)
	if r == nil {
		return em, nil
	}
	mr, err := gomail.CreateReader(r)
	if err != nil {
		return em, fmt.Errorf("create mail reader: %w", err)
	}

	// Extract In-Reply-To and References from headers
	em.InReplyTo = strings.TrimSpace(mr.Header.Get("In-Reply-To"))
	em.References = strings.TrimSpace(mr.Header.Get("References"))
	em.ThreadID = parseThreadID(em.MessageID, em.References)
	if em.InReplyTo != "" && em.ThreadID == "" {
		em.ThreadID = em.InReplyTo
	}
	if em.ThreadID == "" {
		em.ThreadID = em.MessageID
	}

	// Walk parts to find text/plain body
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
// If References contains multiple IDs, the first one is the root.
// If no References, falls back to messageID (new thread).
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

// Send sends an email reply with both plain-text and HTML (Markdown-rendered) parts.
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

	// text/plain part
	pw, _ := mw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"text/plain; charset=UTF-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	qw := quotedprintable.NewWriter(pw)
	qw.Write([]byte(msg.BodyMD))
	qw.Close()

	// text/html part
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
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/mail/... -v -run "TestParseThreadID|TestMarkdownToHTML"
```

Expected: PASS

- [ ] **Step 5: Verify the package compiles**

```bash
go build ./internal/mail/...
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/mail/client.go internal/mail/client_test.go
git commit -m "feat: add mail client with IMAP fetcher and SMTP sender"
```

---

## Chunk 4: Mail Package — Handler and Start Function

### Task 6: Create mail/handler.go with processing logic

**Files:**
- Create: `internal/mail/handler.go`
- Create: `internal/mail/handler_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/mail/handler_test.go`:

```go
package mail

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/robobee/core/internal/model"
)

// --- Stubs ---

type stubSender struct {
	sent []OutgoingEmail
	err  error
}

func (s *stubSender) Send(msg OutgoingEmail) error {
	s.sent = append(s.sent, msg)
	return s.err
}

type stubRouter struct {
	workerID string
	err      error
}

func (r *stubRouter) Route(ctx context.Context, message string) (string, error) {
	return r.workerID, r.err
}

type stubSessionStore struct {
	sessions map[string]*stubSession
}
type stubSession struct {
	workerID        string
	sessionID       string
	lastExecutionID string
}

func newStubSessionStore() *stubSessionStore {
	return &stubSessionStore{sessions: make(map[string]*stubSession)}
}

func (s *stubSessionStore) GetSession(threadID string) (threadSession, error) {
	if sess, ok := s.sessions[threadID]; ok {
		return &mailSessionAdapter{workerID: sess.workerID, sessionID: sess.sessionID, lastExecutionID: sess.lastExecutionID}, nil
	}
	return nil, nil
}

func (s *stubSessionStore) UpsertSession(threadID, workerID, sessionID, lastExecutionID string) error {
	s.sessions[threadID] = &stubSession{workerID: workerID, sessionID: sessionID, lastExecutionID: lastExecutionID}
	return nil
}

type mailSessionAdapter struct {
	workerID        string
	sessionID       string
	lastExecutionID string
}

func (a *mailSessionAdapter) GetWorkerID() string        { return a.workerID }
func (a *mailSessionAdapter) GetSessionID() string       { return a.sessionID }
func (a *mailSessionAdapter) GetLastExecutionID() string { return a.lastExecutionID }

type stubManager struct {
	exec model.WorkerExecution
	err  error
}

func (m *stubManager) ExecuteWorker(ctx context.Context, workerID, input string) (model.WorkerExecution, error) {
	return m.exec, m.err
}

func (m *stubManager) ReplyExecution(ctx context.Context, execID, input string) (model.WorkerExecution, error) {
	return m.exec, m.err
}

func (m *stubManager) GetExecution(execID string) (model.WorkerExecution, error) {
	return m.exec, m.err
}

// --- Tests ---

// TestHandler_Ack verifies that processEmail sends an acknowledgment synchronously.
// Note: process() runs in a goroutine — only the ack is tested here.
func TestHandler_Ack(t *testing.T) {
	sender := &stubSender{}
	h := newTestHandler(
		&stubRouter{workerID: "worker-1"},
		newStubSessionStore(),
		&stubManager{exec: model.WorkerExecution{Status: model.ExecStatusCompleted}},
		sender,
	)

	em := EmailMessage{
		MessageID: "<msg1@example.com>",
		ThreadID:  "<msg1@example.com>",
		From:      "user@example.com",
		Subject:   "Hello",
		Body:      "deploy the app",
	}
	h.reply(em, ackMessage)

	if len(sender.sent) != 1 {
		t.Fatalf("expected 1 sent email (ack), got %d", len(sender.sent))
	}
	if sender.sent[0].BodyMD != ackMessage {
		t.Errorf("expected ack body, got: %s", sender.sent[0].BodyMD)
	}
	if sender.sent[0].To != "user@example.com" {
		t.Errorf("ack sent to wrong address: %s", sender.sent[0].To)
	}
}

// TestHandler_Process_NoWorkerFound calls process() directly (synchronous) to avoid goroutine races.
func TestHandler_Process_NoWorkerFound(t *testing.T) {
	sender := &stubSender{}
	h := newTestHandler(
		&stubRouter{err: errors.New("no workers")},
		newStubSessionStore(),
		&stubManager{},
		sender,
	)

	em := EmailMessage{
		MessageID: "<msg1@example.com>",
		ThreadID:  "<msg1@example.com>",
		From:      "user@example.com",
		Subject:   "Hello",
		Body:      "deploy the app",
	}
	h.process(em)

	if len(sender.sent) != 1 {
		t.Fatalf("expected 1 sent email (error reply), got %d", len(sender.sent))
	}
	if sender.sent[0].BodyMD != noWorkerMsg {
		t.Errorf("expected noWorkerMsg, got: %s", sender.sent[0].BodyMD)
	}
	if sender.sent[0].To != "user@example.com" {
		t.Errorf("reply to wrong address: %s", sender.sent[0].To)
	}
}

// TestHandler_Process_CompletedExecution calls process() directly (synchronous).
func TestHandler_Process_CompletedExecution(t *testing.T) {
	sender := &stubSender{}
	mgr := &stubManager{
		exec: model.WorkerExecution{
			ID:        "exec-1",
			SessionID: "sess-1",
			Status:    model.ExecStatusCompleted,
			Result:    "## Done\nAll good.",
		},
	}
	h := newTestHandler(
		&stubRouter{workerID: "worker-1"},
		newStubSessionStore(),
		mgr,
		sender,
	)

	em := EmailMessage{
		MessageID: "<msg1@example.com>",
		ThreadID:  "<msg1@example.com>",
		From:      "user@example.com",
		Subject:   "Hello",
		Body:      "deploy the app",
	}
	h.process(em)

	if len(sender.sent) != 1 {
		t.Fatalf("expected 1 sent email (result), got %d", len(sender.sent))
	}
	if sender.sent[0].BodyMD != "## Done\nAll good." {
		t.Errorf("unexpected result body: %s", sender.sent[0].BodyMD)
	}
	if sender.sent[0].InReplyTo != "<msg1@example.com>" {
		t.Errorf("InReplyTo not set: %s", sender.sent[0].InReplyTo)
	}
	if sender.sent[0].Subject != "Re: Hello" {
		t.Errorf("unexpected subject: %s", sender.sent[0].Subject)
	}
}

func newTestHandler(router messageRouter, sessionStore mailSessionStoreIface, mgr executionManager, sender EmailSender) *Handler {
	return &Handler{
		router:       router,
		sessionStore: sessionStore,
		manager:      mgr,
		sender:       sender,
		pollInterval: 0,
		pollTimeout:  5 * time.Second,
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/mail/... -v -run "TestHandler_"
```

Expected: FAIL — `Handler` type not defined.

- [ ] **Step 3: Implement handler.go**

Create `internal/mail/handler.go`:

```go
package mail

import (
	"context"
	"log"
	"time"

	"github.com/robobee/core/internal/model"
	"github.com/robobee/core/internal/store"
)

const (
	pollInterval = 2 * time.Second
	pollTimeout  = 30 * time.Minute
	ackMessage   = "⏳ 正在处理，请稍候…"
	errorMessage = "❌ 处理失败，请稍后重试"
	noWorkerMsg  = "❌ 没有找到合适的 Worker，请换个描述试试"
	timeoutMsg   = "⏰ 任务超时，请稍后通过 Web 界面查看结果"
)

// messageRouter abstracts botrouter.Router for testing.
type messageRouter interface {
	Route(ctx context.Context, message string) (string, error)
}

// threadSession abstracts a mail session record.
type threadSession interface {
	GetWorkerID() string
	GetLastExecutionID() string
}

// mailSessionStoreIface abstracts MailSessionStore for testing.
type mailSessionStoreIface interface {
	GetSession(threadID string) (threadSession, error)
	UpsertSession(threadID, workerID, sessionID, lastExecutionID string) error
}

// executionManager abstracts worker.Manager for testing.
type executionManager interface {
	ExecuteWorker(ctx context.Context, workerID, input string) (model.WorkerExecution, error)
	ReplyExecution(ctx context.Context, execID, input string) (model.WorkerExecution, error)
	GetExecution(execID string) (model.WorkerExecution, error)
}

// mailSessionStoreAdapter wraps *store.MailSessionStore to implement mailSessionStoreIface.
type mailSessionStoreAdapter struct {
	inner *store.MailSessionStore
}

func (a *mailSessionStoreAdapter) GetSession(threadID string) (threadSession, error) {
	sess, err := a.inner.GetSession(threadID)
	if err != nil || sess == nil {
		return nil, err
	}
	return sess, nil
}

func (a *mailSessionStoreAdapter) UpsertSession(threadID, workerID, sessionID, lastExecutionID string) error {
	return a.inner.UpsertSession(threadID, workerID, sessionID, lastExecutionID)
}

// Handler processes inbound emails and sends replies.
type Handler struct {
	router       messageRouter
	sessionStore mailSessionStoreIface
	manager      executionManager
	sender       EmailSender
	pollInterval time.Duration
	pollTimeout  time.Duration
}

func NewHandler(
	router messageRouter,
	sessionStore *store.MailSessionStore,
	manager executionManager,
	sender EmailSender,
) *Handler {
	return &Handler{
		router:       router,
		sessionStore: &mailSessionStoreAdapter{inner: sessionStore},
		manager:      manager,
		sender:       sender,
		pollInterval: pollInterval,
		pollTimeout:  pollTimeout,
	}
}

func (h *Handler) processEmail(em EmailMessage) {
	// Send acknowledgment immediately (fire-and-forget, errors logged)
	h.reply(em, ackMessage)

	go h.process(em)
}

func (h *Handler) process(em EmailMessage) {
	ctx := context.Background()

	workerID, err := h.router.Route(ctx, em.Body)
	if err != nil {
		log.Printf("mail: route error: %v", err)
		h.reply(em, noWorkerMsg)
		return
	}

	sess, err := h.sessionStore.GetSession(em.ThreadID)
	if err != nil {
		log.Printf("mail: get session error: %v", err)
		h.reply(em, errorMessage)
		return
	}

	var exec model.WorkerExecution
	if sess != nil && sess.GetLastExecutionID() != "" {
		exec, err = h.manager.ReplyExecution(ctx, sess.GetLastExecutionID(), em.Body)
	} else {
		exec, err = h.manager.ExecuteWorker(ctx, workerID, em.Body)
	}
	if err != nil {
		log.Printf("mail: execute error: %v", err)
		h.reply(em, errorMessage)
		return
	}

	if err := h.sessionStore.UpsertSession(em.ThreadID, workerID, exec.SessionID, exec.ID); err != nil {
		log.Printf("mail: upsert session error: %v", err)
	}

	result := h.waitForResult(exec.ID)
	h.reply(em, result)
}

// waitForResult polls execution status until completion or timeout.
func (h *Handler) waitForResult(executionID string) string {
	deadline := time.Now().Add(h.pollTimeout)
	for time.Now().Before(deadline) {
		exec, err := h.manager.GetExecution(executionID)
		if err != nil {
			log.Printf("mail: poll execution error: %v", err)
			return errorMessage
		}
		switch exec.Status {
		case model.ExecStatusCompleted:
			if exec.Result != "" {
				return exec.Result
			}
			return "✅ 任务已完成"
		case model.ExecStatusFailed:
			return "❌ 任务执行失败: " + exec.Result
		}
		if h.pollInterval > 0 {
			time.Sleep(h.pollInterval)
		}
	}
	return timeoutMsg
}

// reply sends an email reply in the same thread.
func (h *Handler) reply(em EmailMessage, bodyMD string) {
	subject := em.Subject
	if subject != "" && !hasRePrefix(subject) {
		subject = "Re: " + subject
	}

	// Build References for threading: existing refs + the message we're replying to
	refs := em.References
	if em.MessageID != "" {
		if refs != "" {
			refs = refs + " " + em.MessageID
		} else {
			refs = em.MessageID
		}
	}

	if err := h.sender.Send(OutgoingEmail{
		To:         em.From,
		Subject:    subject,
		BodyMD:     bodyMD,
		InReplyTo:  em.MessageID,
		References: refs,
	}); err != nil {
		log.Printf("mail: send reply error: %v", err)
	}
}

func hasRePrefix(subject string) bool {
	return len(subject) >= 3 && (subject[:3] == "Re:" || subject[:3] == "re:")
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/mail/... -v -run "TestHandler_"
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mail/handler.go internal/mail/handler_test.go
git commit -m "feat: add mail handler with thread-based session management"
```

---

### Task 7: Create mail/start.go (polling loop and entry point)

**Files:**
- Create: `internal/mail/start.go`

- [ ] **Step 1: Create start.go**

Create `internal/mail/start.go`:

```go
package mail

import (
	"context"
	"log"
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
			for _, em := range emails {
				handler.processEmail(em)
			}
		}
	}
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./internal/mail/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/mail/start.go
git commit -m "feat: add mail polling loop start function"
```

---

## Chunk 5: Wire Into Server

### Task 8: Wire mail service into main.go

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Add mail import and conditional startup to main.go**

In `cmd/server/main.go`, add `"github.com/robobee/core/internal/mail"` to the import block (alongside feishu and dingtalk).

After the DingTalk block (line ~77), add:

```go
// Start Mail bot if enabled.
if cfg.Mail.Enabled {
    mailSessionStore := store.NewMailSessionStore(db)
    go func() {
        if err := mail.Start(ctx, cfg.Mail, workerStore, mailSessionStore, mgr, aiClient); err != nil {
            log.Printf("mail bot error: %v", err)
        }
    }()
}
```

Note: `ctx` here should be `context.Background()` to match the pattern used for Feishu and DingTalk.

The full block becomes:

```go
// Start Mail bot if enabled.
if cfg.Mail.Enabled {
    mailSessionStore := store.NewMailSessionStore(db)
    go func() {
        if err := mail.Start(context.Background(), cfg.Mail, workerStore, mailSessionStore, mgr, aiClient); err != nil {
            log.Printf("mail bot error: %v", err)
        }
    }()
}
```

- [ ] **Step 2: Verify the server builds**

```bash
go build ./cmd/server/...
```

Expected: no errors.

- [ ] **Step 3: Run all tests**

```bash
go test ./...
```

Expected: all tests PASS (or pre-existing failures unchanged).

- [ ] **Step 4: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat: wire mail bot into server startup"
```

---

### Task 9: Final verification

- [ ] **Step 1: Full build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 2: Full test suite**

```bash
go test ./...
```

Expected: all tests PASS.

- [ ] **Step 3: Verify config example is complete**

```bash
grep -A 8 "^mail:" config.example.yaml
```

Expected output:
```yaml
mail:
  enabled: false
  imap_host: "imap.gmail.com:993"
  smtp_host: "smtp.gmail.com:587"
  username: ""
  password: ""
  poll_interval: "30s"
  mailbox: "INBOX"
```

- [ ] **Step 4: Final commit**

```bash
git add -A
git commit -m "feat: complete mail-based task scheduling integration"
```

---

## Summary

| Task | Files Created/Modified |
|------|----------------------|
| 1 | `go.mod`, `go.sum` |
| 2 | `internal/config/config.go`, `internal/config/config_test.go`, `config.example.yaml` |
| 3 | `internal/store/db.go` |
| 4 | `internal/store/mail_session_store.go`, `internal/store/mail_session_store_test.go` |
| 5 | `internal/mail/client.go`, `internal/mail/client_test.go` |
| 6 | `internal/mail/handler.go`, `internal/mail/handler_test.go` |
| 7 | `internal/mail/start.go` |
| 8 | `cmd/server/main.go` |
| 9 | verification |
