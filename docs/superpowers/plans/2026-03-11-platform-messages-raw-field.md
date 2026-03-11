# Add `raw` Field to `platform_messages` Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `raw TEXT NOT NULL DEFAULT ''` column to `platform_messages` to store original message content with formatting preserved (at-tags, rich text markup).

**Architecture:** The `raw` field flows from platform handlers → `InboundMessage.RawContent` → `MessageStore.Create(raw)` → DB. Each platform sets `RawContent` to the pre-processing text: Feishu stores the original JSON string, DingTalk stores the JSON-marshaled full callback model, Mail stores the body as-is (trimming already happens in IMAP parsing, so `RawContent == Content` for mail).

**Tech Stack:** Go, SQLite (mattn/go-sqlite3), existing Feishu/DingTalk/Mail platform SDKs

---

## Chunk 1: DB Schema + MessageStore

### Task 1: Update schema, `Create()`, and tests

**Files:**
- Modify: `internal/store/db.go`
- Modify: `internal/store/message_store.go`
- Modify: `internal/platform/interfaces.go`
- Modify: `internal/store/message_store_test.go`

- [ ] **Step 1: Write the failing test — update `TestMessageStore_Create` and all other `Create` calls in the test file**

In `internal/store/message_store_test.go`:

1. Update `TestMessageStore_Create` to pass `raw` and verify it's stored:

```go
func TestMessageStore_Create(t *testing.T) {
	s := setupMessageStore(t)
	ctx := context.Background()

	if err := s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello world", `{"text":"hello world"}`); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var raw string
	if err := s.db.QueryRowContext(ctx, `SELECT raw FROM platform_messages WHERE id = ?`, "msg-1").Scan(&raw); err != nil {
		t.Fatalf("query raw: %v", err)
	}
	if raw != `{"text":"hello world"}` {
		t.Errorf("raw: got %q, want %q", raw, `{"text":"hello world"}`)
	}
}
```

2. Update every other `s.Create(...)` call in the file to add `""` as the last argument. These are helper calls that don't test `raw` — passing empty string is fine:

- `TestMessageStore_SetWorkerID`: `s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "")`
- `TestMessageStore_SetStatus`: `s.Create(ctx, "msg-1", "feishu:chat1:userA", "feishu", "hello", "")`
- `TestMessageStore_UpdateStatusBatch`: both calls add `""`
- `TestMessageStore_MarkMerged`: all three calls add `""`
- `TestMessageStore_MarkTerminal_Done`: add `""`
- `TestMessageStore_MarkTerminal_Failed`: add `""`
- `TestMessageStore_GetUnfinished`: all three calls add `""`
- `TestMessageStore_GetSession_AfterExecution`: add `""`
- `TestMessageStore_SetExecution`: add `""`
- `TestMessageStore_GetSession_AfterClear`: add `""`
- `TestMessageStore_GetSession_FirstMessageNoExecution`: add `""`

- [ ] **Step 2: Run test to verify it fails (compile error is expected)**

```bash
cd /Users/tengteng/work/robobee/core && go test ./internal/store/... -run TestMessageStore_Create -v 2>&1 | head -20
```

Expected: compile error — `too many arguments in call to s.Create`

- [ ] **Step 3: Add `raw` column to `CREATE TABLE` in `db.go`**

In `internal/store/db.go`, add `raw TEXT NOT NULL DEFAULT ''` after the `content` line:

```sql
CREATE TABLE IF NOT EXISTS platform_messages (
    id           TEXT PRIMARY KEY,
    session_key  TEXT NOT NULL,
    platform     TEXT NOT NULL,
    worker_id    TEXT NOT NULL DEFAULT '',
    content      TEXT NOT NULL,
    raw          TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'received',
    merged_into  TEXT NOT NULL DEFAULT '',
    execution_id TEXT NOT NULL DEFAULT '',
    session_id   TEXT NOT NULL DEFAULT '',
    received_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    processed_at DATETIME
);
```

Add the migration for existing databases — append to the existing slice in `migrate()`:

```go
for _, stmt := range []string{
    "ALTER TABLE platform_messages ADD COLUMN execution_id TEXT NOT NULL DEFAULT ''",
    "ALTER TABLE platform_messages ADD COLUMN session_id  TEXT NOT NULL DEFAULT ''",
    "ALTER TABLE platform_messages ADD COLUMN raw         TEXT NOT NULL DEFAULT ''",
} {
    db.Exec(stmt) // ignore "duplicate column name" on re-runs
}
```

- [ ] **Step 4: Update `Create()` in `message_store.go`**

Replace the existing `Create` method:

```go
// Create inserts a new message record with status "received".
func (s *MessageStore) Create(ctx context.Context, id, sessionKey, platform, content, raw string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO platform_messages (id, session_key, platform, content, raw) VALUES (?, ?, ?, ?, ?)`,
		id, sessionKey, platform, content, raw,
	)
	return err
}
```

- [ ] **Step 5: Update `MessageStore` interface in `interfaces.go`**

In `internal/platform/interfaces.go`, update the `Create` line in the `MessageStore` interface:

```go
Create(ctx context.Context, id, sessionKey, platform, content, raw string) error
```

- [ ] **Step 6: Run store tests to verify**

```bash
cd /Users/tengteng/work/robobee/core && go test ./internal/store/... -v 2>&1 | tail -20
```

Expected: all store tests PASS. Note: `internal/platform` won't compile yet (manager.go uses old signature) — that's OK, we fix it in the next task.

- [ ] **Step 7: Commit**

```bash
cd /Users/tengteng/work/robobee/core && git add internal/store/db.go internal/store/message_store.go internal/platform/interfaces.go internal/store/message_store_test.go
git commit -m "feat: add raw column to platform_messages and update MessageStore.Create"
```

---

## Chunk 2: InboundMessage + Platform Handlers + Manager

### Task 2: Add `RawContent` to `InboundMessage` and update platform handlers

**Files:**
- Modify: `internal/platform/interfaces.go`
- Modify: `internal/platform/feishu/handler.go`
- Modify: `internal/platform/dingtalk/handler.go`
- Modify: `internal/platform/mail/handler.go`

- [ ] **Step 1: Add `RawContent` field to `InboundMessage` in `interfaces.go`**

In `internal/platform/interfaces.go`, add `RawContent string` after `Content`:

```go
// InboundMessage carries a parsed message from any platform.
type InboundMessage struct {
	Platform   string // "feishu" | "dingtalk" | "mail"
	SenderID   string
	SessionKey string // platform-prefixed session key, e.g. "feishu:chatID:userID"
	Content    string
	RawContent string // original message text with formatting preserved (at-tags, markup)
	Raw        any    // original platform event, used by the sender for reply metadata
}
```

- [ ] **Step 2: Update Feishu handler to set `RawContent`**

In `internal/platform/feishu/handler.go`, set `RawContent: *msg.Content` in the `dispatch` call:

```go
dispatch(platform.InboundMessage{
    Platform:   "feishu",
    SenderID:   senderID,
    SessionKey: "feishu:" + *msg.ChatId + ":" + senderID,
    Content:    text,
    RawContent: *msg.Content,
    Raw:        event,
})
```

`*msg.Content` is the original JSON string from Feishu, e.g. `{"text":"@user hello"}`.

- [ ] **Step 3: Update DingTalk handler to set `RawContent`**

In `internal/platform/dingtalk/handler.go`:

1. Add `"encoding/json"` to the import block.
2. JSON-marshal `data` before the `dispatch` call, with fallback:

```go
rawBytes, err := json.Marshal(data)
rawContent := data.Text.Content
if err != nil {
    log.Printf("dingtalk: failed to marshal raw callback data: %v", err)
} else {
    rawContent = string(rawBytes)
}
msg := platform.InboundMessage{
    Platform:   "dingtalk",
    SenderID:   data.SenderStaffId,
    SessionKey: "dingtalk:" + data.ConversationId + ":" + data.SenderStaffId,
    Content:    text,
    RawContent: rawContent,
    Raw:        data,
}
```

- [ ] **Step 4: Update Mail handler to set `RawContent`**

In `internal/platform/mail/handler.go`, add `RawContent: em.Body` (same as `Content` since trimming happens in IMAP parsing):

```go
dispatch(platform.InboundMessage{
    Platform:   "mail",
    SenderID:   em.From,
    SessionKey: "mail:" + em.ThreadID,
    Content:    em.Body,
    RawContent: em.Body,
    Raw:        em,
})
```

- [ ] **Step 5: Run build to verify platform handlers compile**

```bash
cd /Users/tengteng/work/robobee/core && go build ./internal/platform/... 2>&1
```

Expected: compile error in `manager.go` only — `Create` called with wrong number of args. Handlers themselves should be clean.

### Task 3: Update manager.go to pass `RawContent`

**Files:**
- Modify: `internal/platform/manager.go`

- [ ] **Step 1: Update `msgStore.Create()` call in `manager.go`**

In `internal/platform/manager.go`, line 101, pass `msg.RawContent` as the last argument:

```go
if err := m.msgStore.Create(context.Background(), msgID, msg.SessionKey, msg.Platform, msg.Content, msg.RawContent); err != nil {
    log.Printf("platform[%s]: record message error: %v", p.ID(), err)
}
```

- [ ] **Step 2: Run full build to verify everything compiles**

```bash
cd /Users/tengteng/work/robobee/core && go build ./... 2>&1
```

Expected: clean build, no errors.

- [ ] **Step 3: Run all tests**

```bash
cd /Users/tengteng/work/robobee/core && go test ./... 2>&1
```

Expected: all tests PASS.

- [ ] **Step 4: Commit**

```bash
cd /Users/tengteng/work/robobee/core && git add internal/platform/interfaces.go internal/platform/feishu/handler.go internal/platform/dingtalk/handler.go internal/platform/mail/handler.go internal/platform/manager.go
git commit -m "feat: propagate RawContent through platform handlers into platform_messages.raw"
```
