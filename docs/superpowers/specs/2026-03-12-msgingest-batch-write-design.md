# msgingest: Batch Write After Debounce

**Date:** 2026-03-12
**Status:** Approved

## Problem

`Gateway.Dispatch()` currently writes each inbound message to the database immediately on receipt (via `msgStore.Create()`), then issues additional writes during debounce (`UpdateStatusBatch`) and merge (`MarkMerged`). For a session with N messages in one debounce window, this produces 1 + N + N DB writes. The goal is to collapse all of this into a single batch INSERT that fires only after the debounce timer expires.

## Goals

- One DB write per debounce window (batch INSERT of all accumulated messages).
- Remove the concept of "historical messages" — all messages go through the debounce path uniformly.
- In-memory deduplication replaces the immediate `INSERT OR IGNORE` dedup.

## Non-Goals

- Async/queued writes (all writes remain synchronous within `onDebounce`).
- Changing downstream routing or execution logic.

## Design

### 1. Data Structures

**`debounceState` change:**

```go
type debounceState struct {
    timer      *time.Timer
    generation int
    msgs       []platform.InboundMessage  // full message bodies, arrival order
    content    string                      // merged content string
}
```

`ids []string` and `replyTo` are removed; `ids` are derived from `msgs`, `replyTo` is `msgs[len(msgs)-1]`.

**`Gateway` new field:**

```go
type Gateway struct {
    // ...existing fields...
    seen map[string]struct{}  // in-memory dedup set keyed by platform_msg_id
}
```

**New `BatchMsg` type (in `msgingest` package):**

```go
type BatchMsg struct {
    ID            string
    SessionKey    string
    Platform      string
    Content       string
    Raw           string
    PlatformMsgID string
    MessageTime   int64
    Status        string  // "received" or "merged"
    MergedInto    string  // non-empty only when Status == "merged"
}
```

**`msgingest.MessageStore` interface (simplified):**

```go
type MessageStore interface {
    CreateBatch(ctx context.Context, msgs []BatchMsg) error
}
```

`Create`, `UpdateStatusBatch`, `MarkMerged`, and `MarkTerminal` are removed from this interface. They remain on `store.MessageStore` for use by other packages.

### 2. Gateway Logic

**`Dispatch()` new flow:**

1. If `platform_msg_id` is non-empty and present in `seen` → drop (duplicate), return.
2. If `platform_msg_id` is non-empty → add to `seen`.
3. Detect command → `handleCommand()`.
4. Accumulate into `debounceState.msgs`, merge content, reset timer.

Removed from `Dispatch()`:
- `msgStore.Create()` call.
- Historical message detection (`msgTime > debounce` branch).
- `msgStore.UpdateStatusBatch("debouncing")` call.

**`onDebounce()` new flow:**

1. Take `state.msgs` (arrival order).
2. Generate one `uuid` per message.
3. `primaryID` = ID of the last message.
4. Build `[]BatchMsg`:
   - Messages `[0 … N-2]`: `Status="merged"`, `MergedInto=primaryID`.
   - Message `[N-1]`: `Status="received"`, `MergedInto=""`.
5. Call `msgStore.CreateBatch(ctx, batchMsgs)`.
6. Emit `IngestedMessage{MsgID: primaryID, Content: state.content, ReplyTo: msgs[last], Command: CommandNone}`.

**`handleCommand()` new flow:**

1. Cancel active debounce timer; discard `state.msgs` without writing to DB (`delete(g.sessions, key)`).
2. Generate ID for the command message; write it alone via `msgStore.CreateBatch` (single-element slice, `Status="received"`).
3. Emit command `IngestedMessage`.

### 3. Store Implementation

**`store.MessageStore.CreateBatch()`:**

Executes a single transaction with a multi-value `INSERT`:

```sql
INSERT INTO platform_messages
    (id, session_key, platform, content, raw, platform_msg_id, received_at, status, merged_into)
VALUES
    (?, ?, ?, ?, ?, ?, ?, ?, ?),
    (?, ?, ?, ?, ?, ?, ?, ?, ?),
    ...
```

`merged_into` is an empty string for `status="received"` rows. `received_at` is populated from `MessageTime`; if zero, use `time.Now().UnixMilli()`.

### 4. Test Changes

**Remove** (historical message tests no longer applicable):
- `TestGateway_Historical_EmittedImmediately`
- `TestGateway_Historical_TwoMessages_EmittedIndependently`
- `TestGateway_Historical_DoesNotCancelRunningTimer`
- `TestGateway_RealTime_EntersDebounceNormally`
- `TestGateway_FutureTimestamp_TreatedAsRealTime`

**Update:**
- `mockMsgStore`: replace `Create` + `UpdateStatusBatch` + `MarkMerged` + `MarkTerminal` with `CreateBatch`.
- `TestGateway_Dedup_DropsKnownMessage`: update mock to new interface.
- `TestGateway_Debounce_EmitsSingleMergedMessage`: verify merged content still correct.
- `TestGateway_Command_InterruptsDebounce`: verify debounced messages are NOT written to DB when command arrives.

**Add:**
- `TestGateway_Debounce_BatchWrite`: dispatch 3 messages → verify `CreateBatch` is called once with 2 `merged` rows + 1 `received` row with correct `MergedInto` values.
- `TestGateway_Dedup_InMemory`: dispatch same `platform_msg_id` twice → verify `CreateBatch` called only once.

## DB Schema Impact

`merged_into` column must already exist on `platform_messages`. No migration needed if it does. If not present, add:

```sql
ALTER TABLE platform_messages ADD COLUMN merged_into TEXT NOT NULL DEFAULT '';
```

Verify before implementation.
