# Architecture Redesign: Four-Layer Message Pipeline

**Date:** 2026-03-11
**Status:** Approved
**Scope:** Refactor `internal/platform` into four clearly bounded packages within the same process.

---

## Problem

The current `internal/platform` package is a god object. The `PlatformManager.dispatch` closure (manager.go:69–128) and `Pipeline` (pipeline.go) mix together:

- Platform message reception
- Deduplication and debounce
- AI-based routing
- Serialized worker execution
- Result polling
- Reply sending

Responsibility boundaries are unclear, components are not independently testable, and extending the system (e.g. new command types) requires editing the same large closure.

---

## Solution: Four-Layer Pipeline

Split responsibilities into four packages connected by typed Go channels within the same process (no external message broker).

```
Platform Receivers (Feishu / DingTalk)
         │  platform.InboundMessage  (dispatch callback)
         ▼
┌─────────────────────────┐
│   msgingest.Gateway     │  Layer 1 — 消息接收网关
│  dedup · debounce       │  pkg: internal/msgingest
│  merge · persist        │
└────────────┬────────────┘
             │  chan IngestedMessage
             ▼
┌─────────────────────────┐
│   msgrouter.Gateway     │  Layer 2 — 消息路由网关
│  AI route → workerID    │  pkg: internal/msgrouter
└────────────┬────────────┘
             │  chan RoutedMessage
             ▼
┌─────────────────────────┐
│   dispatcher.Dispatcher │  Layer 3 — 消息调度进程
│  per-session serialize  │  pkg: internal/dispatcher
│  async execution · ACK  │
│  command handling       │
└────────────┬────────────┘
             │  chan SenderEvent
             ▼
┌─────────────────────────┐
│   msgsender.Gateway     │  Layer 4 — 消息发送网关
│  platform lookup · Send │  pkg: internal/msgsender
└─────────────────────────┘
```

Data flows through a single linear channel pipeline. There are no bypass channels or back-references between layers.

---

## Event Types

### `msgingest` output

```go
type CommandType string

const (
    CommandNone  CommandType = ""
    CommandClear CommandType = "clear"
    // extensible: CommandHelp, CommandStatus, …
)

type IngestedMessage struct {
    MsgID      string
    SessionKey string
    Platform   string
    Content    string                    // merged content after debounce window
    ReplyTo    platform.InboundMessage   // carry reply metadata through pipeline
    Command    CommandType               // empty for normal messages
}
```

### `msgrouter` output

```go
type RoutedMessage struct {
    IngestedMessage
    WorkerID string   // empty for command messages (router pass-through)
    RouteErr string   // non-empty if routing failed; dispatcher emits error reply
}
```

### `dispatcher` output

```go
type SenderEventType int

const (
    SenderEventACK    SenderEventType = iota
    SenderEventResult
    SenderEventError
)

type SenderEvent struct {
    Type    SenderEventType
    ReplyTo platform.InboundMessage
    Content string
}
```

---

## Layer Responsibilities

### Layer 1 — `internal/msgingest`

**Interface:**

```go
type Gateway struct { /* msgStore, debounce, sessions map[sessionKey]*debounceState, out chan */ }

func (g *Gateway) Dispatch(msg platform.InboundMessage)
func (g *Gateway) Run(ctx context.Context)
func (g *Gateway) Out() <-chan IngestedMessage
```

**Channel:** `out` is a buffered channel (size 64).

**Responsibilities:**

- Deduplication via `msgStore.Create` (INSERT OR IGNORE); drop duplicate platform messages silently before doing anything else.
- Normal messages: accumulate within debounce window per session key, reset timer on each new message, merge content with `\n\n---\n\n` separator. When the debounce timer fires, persist the merged message (primary ID = last message in batch), then push one `IngestedMessage` to `out`.
- Command messages (e.g. `clear`): **all** command messages interrupt any active debounce window for the same session. `msgingest` itself stops the debounce timer, marks all accumulated-but-not-yet-pushed normal messages as failed in DB, then persists and pushes the command as `IngestedMessage{Command: …}` to `out`. This cancellation is handled entirely within Layer 1 — there is no back-reference from downstream layers. Each accumulated normal message has already been persisted individually by dedup; marking them failed here is a status update only, no orphaned rows.
- Debouncing messages that were not yet merged when the process shut down are in `debouncing` status. `msgStore.GetUnfinished()` returns them; the dispatcher recovers them as normal pending messages on startup.
- Does **not** send ACK. Does **not** route.

**Primary message ID:** the last `MsgID` in the debounce batch is used as the primary tracking ID (consistent with current `session_queue.go:80`).

### Layer 2 — `internal/msgrouter`

**Interface:**

```go
type Gateway struct { /* router, in, out chan */ }

func New(router MessageRouter, in <-chan IngestedMessage) *Gateway
func (g *Gateway) Run(ctx context.Context)
func (g *Gateway) Out() <-chan RoutedMessage
```

**Channel:** `out` is a buffered channel (size 64).

**Responsibilities:**

- Normal messages: call `router.Route(content)` → populate `WorkerID`.
- Command messages (`Command != ""`): skip AI routing, pass through with `WorkerID` empty.
- Route failure: set `RouteErr` to the user-facing error string (sourced from `botrouter` error), pass through with empty `WorkerID`; dispatcher will emit a `SenderEventError` reply.

### Layer 3 — `internal/dispatcher`

**Interface:**

```go
type Dispatcher struct { /* manager, msgStore, in, results(internal chan internalResult), out chan, sessions map[queueKey]*sessionState */ }

func New(manager ExecutionManager, msgStore MessageStore, in <-chan RoutedMessage) *Dispatcher
func (d *Dispatcher) Run(ctx context.Context)
func (d *Dispatcher) Out() <-chan SenderEvent
```

**Channel:** `out` is a buffered channel (size 64).

**Queue key:** `sessionKey + "|" + workerID` (same as current `queueKey()` in `queue_manager.go:30`).

**Responsibilities:**

- Single event loop (`select` over `in`, internal `results` channel, `ctx.Done()`).
- On receiving `RoutedMessage`:
  - If `RouteErr` is set: emit `SenderEvent{SenderEventError, Content: routeErrMsg}` and return.
  - If `Command == CommandClear`: mark all pending+debouncing messages for the session as failed in DB, clear the session state, emit `SenderEvent{SenderEventResult, Content: "✅ 上下文已重置"}`.
  - Otherwise: enqueue under `queueKey(sessionKey, workerID)`. Emit `SenderEvent{SenderEventACK}` immediately upon enqueue (before execution starts, even if the message goes into pending queue). If the queue was idle, also fire execution immediately in a background goroutine; otherwise add to pending queue.
- **Serialization rule:** same `queueKey` → at most one active execution at a time. Different queue keys (different sessions or different workers) run concurrently.
- Async execution: a background goroutine calls `manager.ExecuteWorker` or `manager.ReplyExecution`, then polls `manager.GetExecution` until terminal status (existing `waitForResult` logic). On completion, sends to internal `results` channel.
- On internal result arrival: emit `SenderEvent{SenderEventResult, …}`. If the queue has pending messages, fire the next one immediately.
- **Startup recovery:** call `msgStore.GetUnfinished()` at startup. Clear sentinel rows (no worker_id, status `clear`) are not returned by `GetUnfinished` and are not replayed. Only rows with a valid `worker_id` and status `executing`/`debouncing`/`pending` are recovered.
- **Graceful shutdown:** when `ctx` is cancelled, the event loop exits. In-flight async execution goroutines run to completion (they use `context.Background()` internally, consistent with current behavior); their results are silently dropped after shutdown. This is not a regression — the current system has identical behavior. In-flight executions are recoverable on next startup via `GetUnfinished()`.

**ACK behavior change from current:** the old code sent ACK only for the first message when no session was active. The new design sends ACK for every debounced batch that enters the queue (including messages that arrive while a session is executing and are added to the pending queue). This is intentional — it confirms the message has been accepted for processing.

### Layer 4 — `internal/msgsender`

**Interface:**

```go
type Gateway struct { /* senders map[platformID]PlatformSenderAdapter, in chan */ }

func New(senders map[string]platform.PlatformSenderAdapter, in <-chan SenderEvent) *Gateway
func (g *Gateway) Run(ctx context.Context)
```

**Responsibilities:**

- Consume `SenderEvent` from `in`.
- Look up `PlatformSenderAdapter` by `ReplyTo.Platform`.
- Call `sender.Send(OutboundMessage{…})`.
- Log send errors; do not propagate them upstream.
- Is the **only** component that calls `PlatformSenderAdapter.Send`.

---

## Wiring in `main.go`

```go
ingest  := msgingest.New(msgStore, cfg.MessageQueue.DebounceWindow)
router  := msgrouter.New(botrouter.NewRouter(aiClient, workerStore), ingest.Out())
disp    := dispatcher.New(workerMgr, msgStore, router.Out())
sender  := msgsender.New(sendersByPlatform, disp.Out())

go ingest.Run(ctx)
go router.Run(ctx)
go disp.Run(ctx)
go sender.Run(ctx)

// Each platform receiver calls ingest.Dispatch
if cfg.Feishu.Enabled {
    p := feishu.NewPlatform(cfg.Feishu)
    go p.Receiver().Start(ctx, ingest.Dispatch)
    sendersByPlatform["feishu"] = p.Sender()
}
// same for DingTalk
```

---

## Existing Code Migration

| Current file | Destination |
|---|---|
| `platform/manager.go` — dispatch closure | Logic split across layers; file becomes thin wiring or removed |
| `platform/pipeline.go` — `Route()` | → `msgrouter` internal call |
| `platform/pipeline.go` — `HandleRouted()` + `waitForResult()` | → `dispatcher` async execution goroutine |
| `platform/pipeline.go` — `Handle()` (clear) | → `dispatcher` command branch |
| `platform/queue_manager.go` + `session_queue.go` | → `dispatcher` (same logic, channel-driven event loop) |
| `platform/interfaces.go` — message + adapter types | Stay in `platform` package as shared type definitions |
| `botrouter/router.go` | Unchanged; used by `msgrouter` |
| `worker/manager.go` | Unchanged; used by `dispatcher` |

`internal/platform` package becomes a pure types package: `InboundMessage`, `OutboundMessage`, `PlatformReceiverAdapter`, `PlatformSenderAdapter`, `Platform`.

---

## Error Handling

- **Dedup failure** (DB error on insert): log and drop the message; do not crash.
- **Route failure**: `msgrouter` sets `RouteErr`; `dispatcher` emits `SenderEventError` with user-facing message.
- **Execution failure**: `worker.Manager` marks execution failed; `dispatcher` receives error result and emits `SenderEventError`.
- **Send failure**: `msgsender` logs the error; does not retry (consistent with current behavior).
- **Timeout**: `dispatcher`'s async goroutine enforces the existing 30-minute poll timeout; emits timeout `SenderEventResult`.

---

## Testing

Each layer is independently testable by constructing it with mock dependencies and writing to its input channel:

- `msgingest`: inject mock `msgStore`; call `Dispatch()`; assert messages on `Out()`.
- `msgrouter`: inject mock `router`; write to input channel; assert `WorkerID` on output.
- `dispatcher`: inject mock `ExecutionManager`; write `RoutedMessage`; assert `SenderEvent` sequence (ACK then Result).
- `msgsender`: inject mock `PlatformSenderAdapter`; write `SenderEvent`; assert `Send` calls.

---

## Out of Scope

- No changes to `worker/manager.go`, `botrouter/router.go`, `store/`, or `api/`.
- No changes to platform adapter implementations (`feishu/`, `dingtalk/`).
- No external message broker introduced.
