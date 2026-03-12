# Platform Message Deduplication Design

**Date:** 2026-03-11
**Status:** Approved

## Problem

Feishu and DingTalk may push the same message event more than once due to network retries or at-least-once delivery guarantees. The current system generates a new UUID for every inbound message and has no deduplication check, causing the same message to be routed, queued, and executed multiple times.

Both platforms provide a stable, platform-native message ID per event:
- **Feishu**: `event.Event.Message.MessageId`
- **DingTalk**: `data.MsgId`

## Solution: DB-level Deduplication via UNIQUE Constraint

Use the platform-native message ID as a deduplication key stored in the database. Leverage SQLite's `INSERT OR IGNORE` to atomically reject duplicate inserts. The dispatch path checks whether the insert was accepted and silently drops duplicates before any routing or queuing occurs.

## Design

### 1. Data Layer (`store/db.go`, `store/message_store.go`)

**Schema change** — add `platform_msg_id` column to `platform_messages`:

```sql
platform_msg_id TEXT NOT NULL DEFAULT ''
```

**Unique index** (partial — only enforces uniqueness for non-empty values):

```sql
CREATE UNIQUE INDEX IF NOT EXISTS idx_platform_messages_platform_msg_id
    ON platform_messages(platform_msg_id)
    WHERE platform_msg_id != ''
```

Added idempotently via `ALTER TABLE` in the existing migration compatibility list so existing databases are not broken.

**`MessageStore.Create` signature change:**

```go
// Create inserts a new message. Returns inserted=false (no error) if platform_msg_id
// already exists, indicating a duplicate that should be silently dropped.
Create(ctx context.Context, id, sessionKey, platform, content, raw, platformMsgID string) (inserted bool, err error)
```

Implementation uses `INSERT OR IGNORE` and checks `RowsAffected()`.

**`MessageStore` interface** in `platform/interfaces.go` updated to match new signature.

### 2. Domain Layer (`platform/interfaces.go`)

`InboundMessage` gains one new field:

```go
PlatformMessageID string // platform-native dedup ID; empty string means no dedup
```

### 3. Handler Layer

**Feishu** (`feishu/handler.go`): populate `PlatformMessageID: *msg.MessageId`

**DingTalk** (`dingtalk/handler.go`): populate `PlatformMessageID: data.MsgId`

### 4. Dispatch Layer (`platform/manager.go`)

In the `dispatch` closure, after the existing ACK logic, change the `msgStore.Create` call:

```go
inserted, err := m.msgStore.Create(ctx, msgID, msg.SessionKey, msg.Platform,
    msg.Content, msg.RawContent, msg.PlatformMessageID)
if err != nil {
    log.Printf("platform[%s]: record message error: %v", p.ID(), err)
}
if !inserted {
    log.Printf("platform[%s]: duplicate message dropped platformMsgID=%s", p.ID(), msg.PlatformMessageID)
    return
}
```

No routing, no queuing, no further ACK for duplicates.

**ACK ordering note:** The ACK is sent before `Create` today (to respond quickly). The ACK check uses `queueMgr.IsActiveSession` which is unaffected by dedup — a duplicate that arrives while a session is active would not have sent an ACK anyway. The ordering is preserved.

### 5. Tests

- `store/message_store_test.go`: verify second `Create` with same `platform_msg_id` returns `inserted=false, err=nil`
- `store/message_store_test.go`: verify empty `platform_msg_id` allows multiple inserts (no false dedup)
- `platform/manager_test.go` or integration test: verify duplicate dispatch call results in only one execution

## Scope

- **In scope**: deduplication of platform-pushed duplicate events
- **Out of scope**: content-based dedup (same text sent twice intentionally), dedup across platforms

## Rollout / Migration

The `ALTER TABLE` for `platform_msg_id` is idempotent. Existing rows default to `''`, which is excluded from the unique index. No data migration needed.
