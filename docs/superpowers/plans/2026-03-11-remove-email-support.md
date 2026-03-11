# Remove Email Support Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Completely remove IMAP/SMTP email platform support — all code, config, docs, and dependencies.

**Architecture:** The mail platform is isolated in `internal/platform/mail/`. It plugs into the shared platform manager at startup via `cmd/server/main.go`. Removing it means deleting the package, cleaning up the config struct, removing the startup registration, and running `go mod tidy` to drop now-unused dependencies.

**Tech Stack:** Go, SQLite (via go-sqlite3), YAML config

---

## Chunk 1: Delete mail package and clean up all references

### Task 1: Delete the mail package

**Files:**
- Delete: `internal/platform/mail/handler.go`
- Delete: `internal/platform/mail/client.go`
- Delete: `internal/platform/mail/start.go`
- Delete: `internal/platform/mail/handler_test.go`
- Delete: `internal/platform/mail/client_test.go`

- [ ] **Step 1: Delete all five files in the mail package**

```bash
rm internal/platform/mail/handler.go \
   internal/platform/mail/client.go \
   internal/platform/mail/start.go \
   internal/platform/mail/handler_test.go \
   internal/platform/mail/client_test.go
rmdir internal/platform/mail
```

- [ ] **Step 2: Verify directory is gone**

```bash
ls internal/platform/mail 2>&1
```

Expected: `ls: internal/platform/mail: No such file or directory`

---

### Task 2: Remove MailConfig from config

**Files:**
- Modify: `internal/config/config.go`
- Delete: `internal/config/config_test.go`

- [ ] **Step 1: Remove `Mail MailConfig` field from the `Config` struct**

In `internal/config/config.go`, the `Config` struct currently reads:

```go
type Config struct {
	Server       ServerConfig        `yaml:"server"`
	Database     DatabaseConfig      `yaml:"database"`
	Workers      WorkersConfig       `yaml:"workers"`
	Runtime      RuntimeConfig       `yaml:"runtime"`
	AI           AIConfig            `yaml:"ai"`
	Feishu       FeishuConfig        `yaml:"feishu"`
	DingTalk     DingTalkConfig      `yaml:"dingtalk"`
	Mail         MailConfig          `yaml:"mail"`
	MessageQueue MessageQueueConfig  `yaml:"message_queue"`
}
```

Remove the `Mail` line so it becomes:

```go
type Config struct {
	Server       ServerConfig        `yaml:"server"`
	Database     DatabaseConfig      `yaml:"database"`
	Workers      WorkersConfig       `yaml:"workers"`
	Runtime      RuntimeConfig       `yaml:"runtime"`
	AI           AIConfig            `yaml:"ai"`
	Feishu       FeishuConfig        `yaml:"feishu"`
	DingTalk     DingTalkConfig      `yaml:"dingtalk"`
	MessageQueue MessageQueueConfig  `yaml:"message_queue"`
}
```

- [ ] **Step 2: Remove the `MailConfig` struct**

Remove this entire block from `internal/config/config.go`:

```go
type MailConfig struct {
	Enabled         bool          `yaml:"enabled"`
	IMAPHost        string        `yaml:"imap_host"`
	SMTPHost        string        `yaml:"smtp_host"`
	Username        string        `yaml:"username"`
	Password        string        `yaml:"password"`
	PollInterval    time.Duration `yaml:"poll_interval"`
	Mailbox         string        `yaml:"mailbox"`
	SubjectKeywords []string      `yaml:"subject_keywords"`
}
```

- [ ] **Step 3: Remove the `"time"` import from config.go if it's now unused**

After removing `MailConfig`, check if `time` is still used in `config.go`. It is used in `RuntimeEntry.Timeout` and `MessageQueueConfig.DebounceWindow` — leave the import in place.

- [ ] **Step 4: Delete config_test.go (it only contained TestLoadMailConfig)**

```bash
rm internal/config/config_test.go
```

- [ ] **Step 5: Verify config compiles**

```bash
go build ./internal/config/...
```

Expected: no output (success)

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: remove MailConfig from config"
```

---

### Task 3: Remove mail from cmd/server/main.go

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Remove the `mail` import**

In `cmd/server/main.go`, remove this import line:

```go
"github.com/robobee/core/internal/platform/mail"
```

- [ ] **Step 2: Remove the mail registration block**

Remove these lines:

```go
if cfg.Mail.Enabled {
    platManager.Register(mail.NewPlatform(cfg.Mail))
}
```

- [ ] **Step 3: Verify main compiles**

```bash
go build ./cmd/server/...
```

Expected: no output (success)

- [ ] **Step 4: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat: remove mail platform registration from main"
```

---

### Task 4: Clean up interfaces.go and db.go

**Files:**
- Modify: `internal/platform/interfaces.go`
- Modify: `internal/store/db.go`

- [ ] **Step 1: Remove `"mail"` from the Platform field comment in interfaces.go**

Change line 11 from:

```go
Platform   string // "feishu" | "dingtalk" | "mail"
```

to:

```go
Platform   string // "feishu" | "dingtalk"
```

- [ ] **Step 2: Remove the `DROP TABLE IF EXISTS mail_sessions;` line from db.go**

In `internal/store/db.go`, the `migrate()` function has this block:

```go
DROP TABLE IF EXISTS platform_sessions;
DROP TABLE IF EXISTS feishu_sessions;
DROP TABLE IF EXISTS dingtalk_sessions;
DROP TABLE IF EXISTS mail_sessions;
```

Remove only the `mail_sessions` line so it becomes:

```go
DROP TABLE IF EXISTS platform_sessions;
DROP TABLE IF EXISTS feishu_sessions;
DROP TABLE IF EXISTS dingtalk_sessions;
```

- [ ] **Step 3: Verify everything still builds and tests pass**

```bash
go build ./...
go test ./...
```

Expected: all pass

- [ ] **Step 4: Commit**

```bash
git add internal/platform/interfaces.go internal/store/db.go
git commit -m "feat: remove mail references from interfaces and db migration"
```

---

### Task 5: Remove mail from config files

**Files:**
- Modify: `config.example.yaml`
- Modify: `config.yaml`

- [ ] **Step 1: Remove the `mail:` section from config.example.yaml**

Remove these lines (lines 34–42):

```yaml
mail:
  enabled: false
  imap_host: "imap.gmail.com:993"
  smtp_host: "smtp.gmail.com:587"
  username: ""
  password: ""
  poll_interval: "30s"
  mailbox: "INBOX"
  subject_keywords: []  # 留空则处理所有邮件；非空则只处理标题含任意关键字的邮件（大小写不敏感）
```

- [ ] **Step 2: Remove the `mail:` section from config.yaml**

Remove the entire `mail:` block from the live config (it contains IMAP/SMTP host settings).

- [ ] **Step 3: Commit**

```bash
git add config.example.yaml config.yaml
git commit -m "feat: remove mail config sections"
```

---

### Task 6: Delete old mail docs

**Files:**
- Delete: `docs/superpowers/plans/2026-03-10-mail-task-scheduling.md`
- Delete: `docs/superpowers/specs/2026-03-10-mail-task-scheduling-design.md`

- [ ] **Step 1: Delete both files**

```bash
rm docs/superpowers/plans/2026-03-10-mail-task-scheduling.md \
   docs/superpowers/specs/2026-03-10-mail-task-scheduling-design.md
```

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/plans/2026-03-10-mail-task-scheduling.md \
        docs/superpowers/specs/2026-03-10-mail-task-scheduling-design.md
git commit -m "docs: remove mail task scheduling plan and spec"
```

---

### Task 7: Clean up dependencies

- [ ] **Step 1: Run go mod tidy**

```bash
go mod tidy
```

- [ ] **Step 2: Verify the four mail-only dependencies are gone from go.mod**

```bash
grep -E "go-imap|go-message|go-sasl|goldmark" go.mod
```

Expected: no output (all removed)

- [ ] **Step 3: Verify build and tests still pass**

```bash
go build ./...
go test ./...
```

Expected: all pass

- [ ] **Step 4: Verify no mail/imap/smtp/MailConfig references remain in Go source**

```bash
grep -r --include="*.go" -l "mail\|imap\|smtp\|MailConfig" . \
  --exclude-dir=.git --exclude-dir=.agents
```

Expected: no output

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: remove mail dependencies via go mod tidy"
```
