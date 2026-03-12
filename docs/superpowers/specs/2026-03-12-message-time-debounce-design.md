# Design: Platform Message Time & Debounce Fix

> 2026-03-12

## Problem

`received_at` is currently set to `time.Now().UnixMilli()` at ingestion time, ignoring the platform-native message timestamp. This causes two issues:

1. **Inaccurate `received_at`**: The stored timestamp reflects when the server received the message, not when the user sent it.
2. **Incorrect debounce after restart**: When the application restarts, the platform may push many backlogged messages in rapid succession. Under the current logic, each message resets a new debounce timer (based on server time), causing unnecessary delays for messages that were originally sent far apart.

## Goals

- Store `received_at` as the platform-native message creation time.
- Skip debounce for historical messages (age > debounce window) and emit them immediately and independently.
- Preserve normal debounce behavior for real-time messages.

## Non-Goals

- Merging historical messages from restart recovery with each other.
- Changing debounce duration or configuration.

## Design

### 1. Add `MessageTime` to `platform.InboundMessage`

```go
// platform/interfaces.go
type InboundMessage struct {
    Platform          string
    SenderID          string
    SessionKey        string
    Content           string
    RawContent        string
    Raw               any
    PlatformMessageID string
    MessageTime       int64 // Unix milliseconds from platform; 0 = unknown (fallback to server time)
}
```

### 2. Feishu: parse `event.Event.Message.CreateTime`

The Feishu SDK provides `CreateTime` as `*string` (e.g. `"1609073151345"`), documented as Unix milliseconds. Parse it to `int64`:

```go
// feishu/handler.go  (add "strconv" to imports)
dispatch(platform.InboundMessage{
    ...
    MessageTime: parseMillis(msg.CreateTime),
})

func parseMillis(s *string) int64 {
    if s == nil { return 0 }
    v, err := strconv.ParseInt(*s, 10, 64)
    if err != nil { return 0 }
    return v
}
```

### 3. DingTalk: use `data.CreateAt` directly

`BotCallbackDataModel.CreateAt` is `int64` Unix milliseconds per DingTalk open platform documentation. The stream SDK source does not document the unit in code comments; the millisecond unit is confirmed from the DingTalk open platform API docs (same field documented as `createAt` in webhook payloads).

```go
// dingtalk/handler.go
msg := platform.InboundMessage{
    ...
    MessageTime: data.CreateAt, // int64 Unix ms per DingTalk open platform docs
}
```

### 4. `MessageStore.Create()` accepts external timestamp

Add `messageTime int64` parameter. Use it for `received_at`. Fall back to `time.Now().UnixMilli()` when `messageTime == 0`.

```go
func (s *MessageStore) Create(ctx context.Context, id, sessionKey, platform, content, raw, platformMsgID string, messageTime int64) (bool, error) {
    if messageTime == 0 {
        messageTime = time.Now().UnixMilli()
    }
    result, err := s.db.ExecContext(ctx,
        `INSERT OR IGNORE INTO platform_messages (..., received_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
        id, sessionKey, platform, content, raw, platformMsgID, messageTime,
    )
    ...
}
```

The `MessageStore` interface in `msgingest/gateway.go` must be updated to match. The `mockMsgStore` in `gateway_test.go` must also update its `Create` signature. The mock should store captured calls in a struct slice (e.g. `type createCall struct { id string; messageTime int64 }`) so tests can assert the `messageTime` value passed by `Dispatch()`.

### 5. `gateway.Dispatch()`: skip debounce for historical messages

"Historical" is defined as: `now - msg.MessageTime > debounce`. This means the age threshold is the same value as the debounce duration — a message sent more than one debounce window ago is treated as historical. If debounce is configured to a short value (e.g. 100ms in tests), messages with higher latency may also be treated as historical; this is expected behavior given the definition.

The three steps happen in order after the command check: (1) resolve `msgTime` (`MessageTime` if non-zero, else `time.Now().UnixMilli()`), (2) check age against debounce threshold, (3) enter normal debounce path if not historical. A zero `MessageTime` always resolves to server time, which makes its age ≈ 0 — so it is never treated as historical, which is the correct and intended behavior.

Before entering the debounce state machine, resolve the effective message time and check age:

```go
func (g *Gateway) Dispatch(msg platform.InboundMessage) {
    // ... store + dedup unchanged ...

    if cmd := detectCommand(msg.Content); cmd != CommandNone {
        g.handleCommand(msgID, msg, cmd)
        return
    }

    msgTime := msg.MessageTime
    if msgTime == 0 {
        msgTime = time.Now().UnixMilli()
    }

    // Historical message: skip debounce, emit immediately and independently.
    // Two back-to-back historical messages for the same session are each emitted
    // as separate IngestedMessages without merging.
    if time.Now().UnixMilli()-msgTime > g.debounce.Milliseconds() {
        g.emit(IngestedMessage{
            MsgID:      msgID,
            SessionKey: msg.SessionKey,
            Platform:   msg.Platform,
            Content:    msg.Content,
            ReplyTo:    msg,
            Command:    CommandNone,
        })
        return
    }

    // Normal debounce path (unchanged)...
    g.mu.Lock()
    // ... existing debounce accumulator logic ...
    g.mu.Unlock()
}
```

The `Create()` call in `Dispatch()` must pass `msg.MessageTime` as the new final argument:

```go
inserted, err := g.msgStore.Create(ctx, msgID, msg.SessionKey, msg.Platform, msg.Content, msg.RawContent, msg.PlatformMessageID, msg.MessageTime)
```

**Session state interaction**: if a historical message arrives while a debounce timer is already running for the same session key, the historical message is still emitted immediately without touching the debounce state. The running timer continues and will fire normally when it expires.

**Historical message record status**: after `Create()` inserts the record (status defaults to `"received"`), the historical path returns early, skipping both `UpdateStatusBatch` (which would set status to `"debouncing"`) and `MarkMerged`. The record stays in `"received"` status until downstream routing marks it terminal. This is a distinct code path from the normal debounce path.

**`InsertClearSentinel` is out of scope**: that method inserts sentinel rows that do not correspond to user messages and continues to use `time.Now().UnixMilli()` directly. No change needed there.

**Future timestamps** (clock skew / test data): if `msg.MessageTime > now`, the difference `now - msgTime` is negative, which is less than `debounce.Milliseconds()` (a positive value), so the message correctly falls through to the normal debounce path.

## Behavior Summary

| Scenario | Before | After |
|----------|--------|-------|
| Real-time message | Debounce window from server receive time | Same (age ≤ debounce, normal debounce path) |
| Historical message (age > debounce) | Enters debounce, waits full window | Emitted immediately, no debounce, no merging; `received_at` = platform timestamp (old) |
| Two back-to-back historical messages, same session | Both enter debounce; second resets timer; may merge if within debounce window | Each emitted independently (historical path, no merging) |
| Historical arrives while debounce timer running | Resets the debounce timer | Emitted immediately; existing timer continues unchanged |
| `MessageTime == 0` | Uses server time | Falls back to server time (unchanged behavior) |
| `MessageTime` in the future | N/A | Treated as real-time (negative age < debounce) |
| `received_at` | Server ingestion time | Platform message creation time |

## Affected Files

- `internal/platform/interfaces.go` — add `MessageTime int64`
- `internal/platform/feishu/handler.go` — parse `CreateTime`, add `parseMillis` helper, add `strconv` import
- `internal/platform/dingtalk/handler.go` — set `MessageTime = data.CreateAt`
- `internal/store/message_store.go` — `Create()` accepts `messageTime int64`
- `internal/msgingest/gateway.go` — `MessageStore` interface update + historical message check in `Dispatch()`
- `internal/msgingest/gateway_test.go` — update `mockMsgStore.Create` signature + add historical/real-time debounce tests
- `internal/store/message_store_test.go` — update `Create()` call sites + add `received_at` assertion

## Testing

- Unit test: real-time message (age < debounce) enters debounce normally.
- Unit test: message with age > debounce is emitted immediately without entering debounce state.
- Unit test: two back-to-back historical messages for same session are emitted as two separate IngestedMessages.
- Unit test: historical message arriving while debounce timer is running does not cancel the timer.
- Unit test (`message_store_test.go`): when a non-zero `messageTime` is passed to `Create()`, `received_at` equals that value exactly (not server time). The existing `TestMessageStore_Create_ReceivedAtMillisecondPrecision` test (which passes `0` and asserts `received_at > 0`) must be preserved alongside the new test.
- Unit test (`message_store_test.go`): when `messageTime == 0` is passed to `Create()`, `received_at` is set to a server-time value (> 0, near `time.Now()`).
- Unit test: future `MessageTime` falls through to normal debounce path.
