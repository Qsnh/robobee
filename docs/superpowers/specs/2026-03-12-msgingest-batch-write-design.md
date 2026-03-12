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

**Invariant:** `len(msgs) >= 1` whenever a `debounceState` entry is present in `g.sessions`. The timer callback guards `len(msgs) == 0` as an early-return safety check (matching the existing guard on `len(state.ids) == 0`).

**`New()` constructor:** Must initialize both `sessions` and `seen`:
```go
return &Gateway{
    msgStore: msgStore,
    debounce: debounce,
    sessions: make(map[string]*debounceState),
    seen:     make(map[string]struct{}),
    out:      make(chan IngestedMessage, 64),
}
```

**`Gateway` new field:**

```go
type Gateway struct {
    // ...existing fields...
    seen map[string]struct{}  // in-memory dedup set keyed by platform_msg_id
}
```

**Lifecycle of `seen`:** The `seen` map grows for the lifetime of the process. This is intentional and acceptable at current scale — the set is bounded by the number of unique messages received per process lifetime. Messages with an empty `platform_msg_id` are never added to `seen` (they are always allowed through). When a command interrupts a debounce window, the `seen` entries for the discarded normal messages are **not** removed — this prevents replays of those platform messages from re-entering the system within the same process lifetime. If replay of interrupted messages is needed, a process restart clears the set.

**Command messages with empty `platform_msg_id`:** If a command message arrives with an empty `platform_msg_id`, it bypasses the `seen` check and is always processed. Rapid platform retries of the same command could be processed multiple times. This is acceptable because commands are idempotent (a double `clear` is harmless); no mitigation is required.

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
    CreateBatch(ctx context.Context, msgs []BatchMsg) (int64, error)
}
```

The `int64` return value is the number of rows actually inserted (from `RowsAffected`), used by the caller to detect whether the primary row was silently ignored by `INSERT OR IGNORE`.

`Create`, `UpdateStatusBatch`, `MarkMerged`, and `MarkTerminal` are removed from this interface. They remain on `store.MessageStore` for use by other packages.

### 2. Gateway Logic

**`Dispatch()` new flow:**

All steps below are executed while holding `g.mu`. The lock is acquired at the start of `Dispatch()` and released after step 4 (matching the existing lock scope around debounce state mutations).

1. If `platform_msg_id` is non-empty and present in `seen` → drop (duplicate), unlock, return.
2. If `platform_msg_id` is non-empty → add to `seen`.
3. Detect command → unlock, call `handleCommand()`.
4. Accumulate into `debounceState.msgs`, merge content, reset timer, then unlock.

Removed from `Dispatch()`:
- `msgStore.Create()` call.
- Historical message detection (`msgTime > debounce` branch).
- `msgStore.UpdateStatusBatch("debouncing")` call.

**`onDebounce()` new flow:**

1. Guard: if `len(state.msgs) == 0`, release lock and return.
2. Take `state.msgs` (arrival order), length N.
3. Generate one `uuid` per message.
4. `primaryID` = ID of `msgs[N-1]` (the last/most-recent message). This is the message whose ID is emitted downstream and whose row holds the merged content. The primary row gets `status="received"`.

   **Behavioral change vs. old code:** In the old `onDebounce`, `primaryID = ids[len(ids)-1]` (the last/surviving message) was passed to `MarkMerged(primaryID, mergedIDs)`. `MarkMerged` set `status='merged'` on **both** the primary row and all merged rows. Under the new design, the primary row gets `status="received"` and only the earlier messages get `status="merged"`. This is a deliberate change — the primary row's status now reflects that it is the active message awaiting processing. No downstream code identifies the primary by its `status='merged'`; it is always identified by its `MsgID` in the emitted `IngestedMessage`.
5. Build `[]BatchMsg`:
   - `msgs[0 … N-2]` (earlier messages): `Status="merged"`, `MergedInto=primaryID`.
   - `msgs[N-1]` (last message, the primary): `Status="received"`, `MergedInto=""`.
   - **When N=1:** the merged slice is empty; `CreateBatch` is called with a single `received` element. No `merged` rows are written.
6. Call `msgStore.CreateBatch(ctx, batchMsgs)`.
   - **On error:** log the error and return **without emitting**. The messages are lost for this session; downstream routing is NOT triggered. Emitting an `IngestedMessage` whose row does not exist in the DB would cause silent no-ops in all downstream `UPDATE WHERE id = ?` calls.
   - **If any row is silently ignored by `INSERT OR IGNORE`:** Within a single process lifetime, `seen` prevents any message from reaching the debounce accumulator twice, so all `platform_msg_id` values in a batch are distinct and guaranteed not to collide with each other. A collision can only happen against a row written by a *previous process run* (before `seen` was reset). Implementation: after `CreateBatch`, compare `rowsInserted` to `int64(N)`. If `rowsInserted != int64(N)`, at least one row was silently ignored — log and return without emitting. **Conservative trade-off:** This guard cannot distinguish "primary ignored" from "only a merged row ignored." Suppressing the emit when only a merged row collides is overly conservative (the primary is valid), but it is the safe choice: emitting with a partial write leaves dangling `merged_into` references. In practice, post-restart partial collisions require the exact same debounce window to be re-delivered with exactly the same messages except the primary — an extremely unlikely scenario. The conservative behavior is accepted. Note: `rowsInserted == 0` as the threshold is incorrect for N > 1; `rowsInserted != int64(N)` is the correct check.
7. Emit `IngestedMessage{MsgID: primaryID, Content: state.content, ReplyTo: msgs[N-1], Command: CommandNone}`.

**`handleCommand()` new flow:**

`handleCommand` is called from `Dispatch()` **after** `g.mu` is released (step 3 of `Dispatch`). All mutations to `g.sessions` inside `handleCommand` must reacquire `g.mu`.

**Known race:** Between `Dispatch` releasing the lock and `handleCommand` reacquiring it, another concurrent `Dispatch` call for the same session could accumulate a new message and start a new timer. `handleCommand` will then cancel that timer and delete those messages without writing them to DB. This race exists in the current codebase as well and is accepted behavior — the window is a fraction of a goroutine schedule and the consequence (silently dropping a message that arrived simultaneously with a clear command) is acceptable given that a `clear` command semantically resets the session.

1. Acquire `g.mu`. Cancel active debounce timer; call `delete(g.sessions, key)` to discard `state.msgs` without writing them to DB. Release `g.mu`. Their `seen` entries are retained (see above).
2. Generate ID for the command message; write it alone via `msgStore.CreateBatch` (single-element slice, `Status="received"`).
   - **On error:** log the error and return **without emitting**. This is consistent with the normal debounce error policy.
3. Emit command `IngestedMessage`.

**Relationship to `InsertClearSentinel`:** `handleCommand` writes the command message row with `status="received"`. Downstream handlers that process `CommandClear` call `InsertClearSentinel` separately to insert a distinct `status="clear"` sentinel row. These are two different rows with two different IDs — this matches existing behavior and is unchanged by this design.

### 3. Store Implementation

**`store.MessageStore.CreateBatch()`:**

Executes a single transaction with a multi-value `INSERT OR IGNORE`:

```sql
INSERT OR IGNORE INTO platform_messages
    (id, session_key, platform, content, raw, platform_msg_id, received_at, status, merged_into)
VALUES
    (?, ?, ?, ?, ?, ?, ?, ?, ?),
    (?, ?, ?, ?, ?, ?, ?, ?, ?),
    ...
```

`INSERT OR IGNORE` is used to safely handle the edge case where two messages in the same debounce batch share a `platform_msg_id`. Rows with a duplicate `platform_msg_id` are silently skipped; the partial unique index `WHERE platform_msg_id != ''` already exists on the table.

Returns `(int64, error)` — the `int64` is the number of rows actually inserted (from `sql.Result.RowsAffected()`). Callers use this to detect whether the primary row was silently ignored (see Section 2, step 6).

`merged_into` is an empty string for `status="received"` rows. `received_at` is populated from `MessageTime`; if zero, use `time.Now().UnixMilli()`.

### 4. DB Schema Impact

**`merged_into` column:** Already present in the initial schema (migration v1, `db.go` line 63: `merged_into TEXT NOT NULL DEFAULT ''`). No schema change required.

**`status = 'debouncing'`:** Under the new design, no rows will ever be written with `status='debouncing'`. Any existing rows in a production database with `status='debouncing'` (written before this change) will be orphaned — they will not be returned by `GetUnfinished` startup recovery. Add a migration step:

```sql
UPDATE platform_messages SET status = 'failed' WHERE status = 'debouncing';
```

Run this migration before deploying the new code. After deployment, also remove `'debouncing'` and `'merged'` from the `GetUnfinished` status filter in `store.MessageStore`: under the new design, merged rows always have `worker_id=''` so they are already excluded by the `AND worker_id != ''` clause; removing the status values makes the intent explicit.

### 5. Test Changes

**Remove** (historical message tests no longer applicable):
- `TestGateway_Historical_EmittedImmediately`
- `TestGateway_Historical_TwoMessages_EmittedIndependently`
- `TestGateway_Historical_DoesNotCancelRunningTimer`
- `TestGateway_RealTime_EntersDebounceNormally`
- `TestGateway_FutureTimestamp_TreatedAsRealTime`

**Update:**
- `mockMsgStore`: replace `Create` + `UpdateStatusBatch` + `MarkMerged` + `MarkTerminal` with `CreateBatch(ctx, msgs []BatchMsg) (int64, error)`.
- `TestGateway_Dedup_DropsKnownMessage`: update mock to new interface.
- `TestGateway_Debounce_EmitsSingleMergedMessage`: verify merged content still correct.
- `TestGateway_Command_InterruptsDebounce`: verify debounced messages are NOT written to DB when command arrives; verify command message IS written via `CreateBatch`.

**Add:**
- `TestGateway_Debounce_BatchWrite`: dispatch 3 messages → verify `CreateBatch` called once with 2 `merged` rows (pointing to primary ID) + 1 `received` row; verify emitted `MsgID` equals primary ID.
- `TestGateway_Debounce_SingleMessage`: dispatch 1 message → verify `CreateBatch` called with exactly 1 `received` row (no `merged` rows).
- `TestGateway_Dedup_InMemory`: dispatch same `platform_msg_id` twice within one debounce window → verify `CreateBatch` called only once with 1 row.
- `TestGateway_BatchWrite_Error_NormalPath`: mock `CreateBatch` returns an error in debounce → verify nothing is emitted on `Out()`.
- `TestGateway_BatchWrite_Error_CommandPath`: mock `CreateBatch` returns an error in command path → verify nothing is emitted on `Out()`.
- `TestGateway_BatchWrite_PartialInsert`: dispatch 3 messages; mock `CreateBatch` returns `rowsInserted=2` (N-1, simulating the primary being silently ignored while 2 merged rows succeeded) → verify nothing is emitted on `Out()`.
