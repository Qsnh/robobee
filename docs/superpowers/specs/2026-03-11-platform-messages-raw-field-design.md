# Design: Add `raw` Field to `platform_messages`

**Date:** 2026-03-11
**Status:** Approved

## Summary

Add a `raw TEXT NOT NULL DEFAULT ''` column to `platform_messages` to preserve the original message content with formatting intact (Markdown, at-tags, rich text markup), distinct from the existing `content` field which stores processed plain text.

## Background

The `content` field currently stores normalized plain text extracted from platform-specific payloads. This loses original formatting information such as @-mention markup in Feishu, at-user data in DingTalk, and unstripped whitespace in Mail. A `raw` field provides an immutable record of the original message content.

## Data Model

### Schema Change

```sql
-- In CREATE TABLE
raw TEXT NOT NULL DEFAULT ''

-- Migration for existing databases (idempotent)
ALTER TABLE platform_messages ADD COLUMN raw TEXT NOT NULL DEFAULT ''
```

### Field Semantics

| Field | Value |
|---|---|
| `content` | Processed plain text (current behavior, unchanged) |
| `raw` | Original message content with formatting preserved |

Empty string (`''`) for `raw` means the message was created before this field was introduced.

## Component Changes

### 1. `internal/store/db.go`

- Add `raw TEXT NOT NULL DEFAULT ''` to `CREATE TABLE IF NOT EXISTS platform_messages`
- Add `ALTER TABLE platform_messages ADD COLUMN raw TEXT NOT NULL DEFAULT ''` to the idempotent migration block

### 2. `internal/platform/interfaces.go`

Add `RawContent string` to `InboundMessage`:

```go
type InboundMessage struct {
    Platform   string
    SenderID   string
    SessionKey string
    Content    string
    RawContent string      // NEW: original message text with formatting
    Raw        interface{} // existing: full platform event for reply routing
}
```

### 3. Platform Handlers

Each handler sets both `Content` (processed plain text) and `RawContent` (original):

| Platform | File | `Content` | `RawContent` |
|---|---|---|---|
| Feishu | `internal/platform/feishu/handler.go` | `content["text"]` (JSON-extracted plain text) | `*msg.Content` (original JSON string, e.g. `{"text":"@mention hello"}`) |
| DingTalk | `internal/platform/dingtalk/handler.go` | `strings.TrimSpace(data.Text.Content)` | `data` JSON-marshaled (preserves AtUsers, ConversationId, etc.) |
| Mail | `internal/platform/mail/handler.go` | `em.Body` after TrimSpace | `em.Body` before TrimSpace |

### 4. `internal/platform/manager.go`

Pass `msg.RawContent` to `msgStore.Create()`:

```go
if err := m.msgStore.Create(ctx, msgID, msg.SessionKey, msg.Platform, msg.Content, msg.RawContent); err != nil {
    log.Printf(...)
}
```

### 5. `internal/store/message_store.go`

Update `Create()` signature and INSERT:

```go
func (s *MessageStore) Create(ctx context.Context, id, sessionKey, platform, content, raw string) error {
    _, err := s.db.ExecContext(ctx,
        `INSERT INTO platform_messages (id, session_key, platform, content, raw) VALUES (?, ?, ?, ?, ?)`,
        id, sessionKey, platform, content, raw,
    )
    return err
}
```

### 6. Tests (`internal/store/message_store_test.go`)

Update all `Create()` calls to pass an additional `raw` string argument.

## Other Call Sites

- `InsertClearSentinel()` also inserts into `platform_messages` but requires no change — the `DEFAULT ''` handles the `raw` column automatically.
- The `MessageStore` interface in `internal/platform/interfaces.go` defines `Create` and must be updated alongside the concrete implementation (caught at compile time if missed).

## Error Handling

- DingTalk JSON marshal errors: log the error and fall back to `data.Text.Content` as `RawContent`
- No other error cases — `raw` defaults to `''` if not provided

## Migration

Uses the same idempotent `ALTER TABLE ... IGNORE DUPLICATE COLUMN` pattern as `execution_id` and `session_id`. Existing rows get `raw = ''`.
