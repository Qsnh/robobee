# Remove Email Support

**Date:** 2026-03-11

## Goal

Completely remove email (IMAP/SMTP) platform support from the codebase. No dead code, no disabled flags — full deletion.

## Scope

### Files to delete

- `internal/platform/mail/handler.go`
- `internal/platform/mail/client.go`
- `internal/platform/mail/start.go`
- `internal/platform/mail/handler_test.go`
- `internal/platform/mail/client_test.go`
- `docs/superpowers/plans/2026-03-10-mail-task-scheduling.md`
- `docs/superpowers/specs/2026-03-10-mail-task-scheduling-design.md`

### Files to modify

- `internal/config/config.go` — remove `MailConfig` struct and `Mail MailConfig` field from `Config`
- `internal/config/config_test.go` — remove any test cases referencing `MailConfig` or `Mail`
- `cmd/server/main.go` — remove `mail` import and `cfg.Mail.Enabled` registration block
- `config.example.yaml` — remove `mail:` configuration section
- `config.yaml` — remove `mail:` configuration section (live config)
- `internal/platform/interfaces.go` — remove `"mail"` from the `Platform` field comment
- `internal/store/db.go` — remove `DROP TABLE IF EXISTS mail_sessions;` migration line

### Dependency cleanup

Run `go mod tidy` after deletion to remove the three dependencies used exclusively by the mail package:

- `github.com/emersion/go-imap`
- `github.com/emersion/go-message`
- `github.com/emersion/go-sasl` (indirect dep of go-imap)
- `github.com/yuin/goldmark`

## Verification

After changes:

1. `go build ./...` passes with no errors
2. `go test ./...` passes
3. `go mod tidy` produces no diff (dependencies already cleaned)
4. No references to `mail`, `imap`, `smtp`, or `MailConfig` remain in Go source files
