# Mail-Based Task Scheduling Design

**Date:** 2026-03-10
**Status:** Approved

## Overview

Add email-based task scheduling to the existing system that already supports Feishu and DingTalk bots. Users send emails to trigger worker executions; the system replies with results.

## Architecture

Follows the same three-layer pattern as existing Feishu/DingTalk integrations:

```
internal/mail/
  client.go   — IMAP polling loop + SMTP sender
  handler.go  — message processing (route → execute → reply)

internal/store/
  mail_session_store.go  — thread-based session persistence
```

## Message Flow

1. Timer (configurable interval) triggers IMAP poll; reads unseen messages
2. Parse sender, subject, body, `Message-ID` / `In-Reply-To` / `References` headers
3. Mark message as seen on IMAP server
4. Send immediate acknowledgment reply: "⏳ 正在处理，请稍候…"
5. `botrouter.Route(message)` selects matching worker
6. Look up or create session (thread-based); call `Manager.ExecuteWorker()` or `Manager.ReplyExecution()`
7. Poll execution status every 2s (30-minute timeout)
8. Convert result Markdown → HTML; send via SMTP as reply in same thread

## Session Management

**Thread identification:**
- New email (no `In-Reply-To`): create new session, use `Message-ID` as `thread_id`
- Reply email (has `In-Reply-To`): walk `References` header chain to find the root `Message-ID` as `thread_id`

**Database table:** `mail_sessions`

| Column     | Type              | Notes                          |
|------------|-------------------|--------------------------------|
| thread_id  | TEXT PRIMARY KEY  | Root Message-ID of thread      |
| session_id | TEXT              | Claude session ID              |
| created_at | DATETIME          |                                |
| updated_at | DATETIME          |                                |

**Reply headers:** All outgoing replies set `In-Reply-To` and `References` so mail clients group them into the same thread.

## Configuration

```yaml
mail:
  enabled: true
  imap_host: "imap.gmail.com:993"
  smtp_host: "smtp.gmail.com:587"
  username: "bot@example.com"
  password: "xxx"
  poll_interval: "30s"   # supports Go duration strings: 10s, 1m, etc.
  mailbox: "INBOX"
```

## Dependencies

- `github.com/emersion/go-imap` — IMAP client
- `github.com/emersion/go-message` — MIME parsing
- Standard library `net/smtp` — SMTP sending
- `github.com/yuin/goldmark` — Markdown → HTML conversion

## Integration Points

- `cmd/server/main.go` — conditional startup (same pattern as Feishu/DingTalk)
- `internal/config/config.go` — add `MailConfig` struct
- `internal/store/db.go` — add `mail_sessions` migration
- `internal/botrouter/router.go` — reused as-is (no changes)
- `internal/worker/manager.go` — reused as-is (no changes)

## Error Handling

- IMAP connection failure: log error, retry on next poll interval
- SMTP send failure: log error (execution result still recorded in DB)
- No matching worker: reply with error message explaining no worker matched
- Execution timeout (30 min): reply with timeout notification
