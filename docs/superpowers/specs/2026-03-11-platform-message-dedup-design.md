# Platform Message Deduplication Design

**Date:** 2026-03-11
**Status:** Draft

## Problem

Feishu and DingTalk may push the same message event more than once due to network retries or at-least-once delivery guarantees. The current system generates a new UUID for every inbound message and has no deduplication check, causing the same message to be routed, queued, and executed multiple times.

Both platforms provide a stable, platform-native message ID per event:
- **Feishu**: `event.Event.Message.MessageId` (type `*string`)
- **DingTalk**: `data.MsgId` (type `string`, confirmed in `BotCallbackDataModel` via `go doc`)

## Solution: DB-level Deduplication via UNIQUE Constraint

Use the platform-native message ID as a deduplication key stored in the database. Leverage SQLite's `INSERT OR IGNORE` to atomically reject duplicate inserts. The dispatch path checks whether the insert was accepted and silently drops duplicates before any routing or queuing occurs.

## Design

### 1. Data Layer (`store/db.go`, `store/message_store.go`)

**Schema change** — `platform_messages` table gains a new column:

```sql
platform_msg_id TEXT NOT NULL DEFAULT ''
```

**Unique partial index** (enforces uniqueness only for non-empty values, so existing rows with `''` and clear sentinels are unaffected):

```sql
CREATE UNIQUE INDEX IF NOT EXISTS idx_platform_messages_platform_msg_id
    ON platform_messages(platform_msg_id)
    WHERE platform_msg_id != ''
```

**Migration strategy:**
- The `CREATE UNIQUE INDEX IF NOT EXISTS` statement is placed in the main schema block in `db.go` (safe for new databases since `CREATE TABLE IF NOT EXISTS` is idempotent).
- The column is added to the idempotent `ALTER TABLE` compatibility list for existing databases:
  ```go
  "ALTER TABLE platform_messages ADD COLUMN platform_msg_id TEXT NOT NULL DEFAULT ''"
  ```
- The index is also added to the compatibility list for existing databases:
  ```go
  "CREATE UNIQUE INDEX IF NOT EXISTS idx_platform_messages_platform_msg_id ON platform_messages(platform_msg_id) WHERE platform_msg_id != ''"
  ```

**`InsertClearSentinel` compatibility:** Clear sentinel rows always have `platform_msg_id = ''` (the default). Because the unique index is a partial index that excludes `''`, multiple clear sentinels for different sessions can coexist without conflict. `InsertClearSentinel` requires no change.

**`MessageStore.Create` signature change:**

```go
// Create inserts a new message. Returns inserted=false (no error) if a record
// with the same non-empty platform_msg_id already exists (duplicate event).
// If platform_msg_id is empty, the insert always proceeds.
Create(ctx context.Context, id, sessionKey, platform, content, raw, platformMsgID string) (inserted bool, err error)
```

Implementation uses `INSERT OR IGNORE` and checks `result.RowsAffected() == 1`.

**`MessageStore` interface** in `platform/interfaces.go` updated to match the new signature. All existing mock structs implementing this interface (in test files) must also be updated to match.

**Index growth:** The unique index grows over time since entries are never pruned. Platform retry windows are bounded (Feishu retries for up to 1 hour), so old entries are safe to prune, but periodic cleanup is out of scope for this change. The index growth is acceptable for current message volumes.

### 2. Domain Layer (`platform/interfaces.go`)

`InboundMessage` gains one new field:

```go
PlatformMessageID string // platform-native dedup ID; empty string means no dedup
```

### 3. Handler Layer

**Feishu** (`feishu/handler.go`): populate using the existing `derefStr` helper to guard against nil:
```go
PlatformMessageID: derefStr(msg.MessageId),
```
If `MessageId` is nil, this yields `''`, which bypasses dedup (the insert proceeds without uniqueness enforcement). This is the safe fallback.

**DingTalk** (`dingtalk/handler.go`): populate directly (field is a non-pointer `string`):
```go
PlatformMessageID: data.MsgId,
```

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

No routing, no queuing, no further processing for duplicates.

**ACK behavior on duplicates:**
- If session is not active: ACK is sent before `Create`. A duplicate would receive an ACK but then be dropped before routing. This is acceptable — a redundant ACK is harmless.
- If session is active: no ACK is sent (existing behavior), and the duplicate is dropped at `Create`. No double-ACK leakage.

### 5. Call Sites to Update

All code that calls `msgStore.Create` must pass the new `platformMsgID string` argument:
- `internal/platform/manager.go` — the only production call site

All types implementing the `MessageStore` interface must add the new parameter:
- Any mock structs in test files (e.g. in `platform/manager_test.go`, `platform/pipeline_test.go`)

### 6. Tests

**`store/message_store_test.go`:**
- Verify second `Create` with same non-empty `platform_msg_id` returns `inserted=false, err=nil`
- Verify first `Create` with non-empty `platform_msg_id` returns `inserted=true, err=nil`
- Verify `Create` with empty `platform_msg_id` (called twice) returns `inserted=true` both times (no false dedup)
- Verify `InsertClearSentinel` still works after schema change (clear rows have `platform_msg_id=''` by default)

**`platform/manager_test.go` (unit test with mock `MessageStore`):**
- When mock `Create` returns `inserted=false`: verify routing is not called and no message is enqueued
- When mock `Create` returns `inserted=true`: verify normal routing and enqueue flow proceeds
- Verify that a duplicate arriving on a non-active session does not trigger a second routing call (ACK may still be sent, which is acceptable)

## Scope

- **In scope**: deduplication of platform-pushed duplicate events using platform-native message IDs
- **Out of scope**: content-based dedup, dedup across platforms, periodic cleanup of old `platform_msg_id` entries
