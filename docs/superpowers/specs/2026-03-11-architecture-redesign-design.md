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
type Gateway struct { /* msgStore, debounce, sessions map, out chan */ }

func (g *Gateway) Dispatch(msg platform.InboundMessage)
func (g *Gateway) Run(ctx context.Context)
func (g *Gateway) Out() <-chan IngestedMessage
```

**Responsibilities:**

- Deduplication via `msgStore.Create` (INSERT OR IGNORE); drop duplicate platform messages silently.
- Normal messages: accumulate within debounce window, merge content, persist the merged message, then push one `IngestedMessage` to `out`.
- Command messages (e.g. `clear`): skip debounce, persist immediately, push `IngestedMessage{Command: CommandClear}` to `out`.
- Does **not** send ACK. Does **not** route.

### Layer 2 — `internal/msgrouter`

**Interface:**

```go
type Gateway struct { /* router, in, out chan */ }

func New(router MessageRouter, in <-chan IngestedMessage) *Gateway
func (g *Gateway) Run(ctx context.Context)
func (g *Gateway) Out() <-chan RoutedMessage
```

**Responsibilities:**

- Normal messages: call `router.Route(content)` → populate `WorkerID`.
- Command messages (`Command != ""`): skip AI routing, pass through with `WorkerID` empty.
- Route failure: set `RouteErr`, pass through; dispatcher will emit an error reply.

### Layer 3 — `internal/dispatcher`

**Interface:**

```go
type Dispatcher struct { /* manager, msgStore, in, results(internal), out chan, sessions map */ }

func New(manager ExecutionManager, msgStore MessageStore, in <-chan RoutedMessage) *Dispatcher
func (d *Dispatcher) Run(ctx context.Context)
func (d *Dispatcher) Out() <-chan SenderEvent
```

**Responsibilities:**

- Single event loop (`select` over `in`, internal `results` channel, `ctx.Done()`).
- On receiving `RoutedMessage`:
  - If `RouteErr` is set: emit `SenderEvent{SenderEventError, …}`.
  - If `Command == CommandClear`: cancel all pending messages for the session, emit clear reply via `SenderEvent`.
  - Otherwise: enqueue for the session+worker pair, emit `SenderEvent{SenderEventACK, …}` immediately to confirm entry into queue.
- Per-session serialization: one execution in-flight per session+worker at a time; subsequent messages queue as pending.
- Async execution: fire `manager.ExecuteWorker` or `manager.ReplyExecution` in a goroutine; result arrives on internal `results` channel (no blocking slot).
- On result arrival: emit `SenderEvent{SenderEventResult, …}`, then fire next pending message if any.
- Startup recovery: call `msgStore.GetUnfinished()` to restore in-progress sessions (same logic as current `RecoverFromDB`).

**Serialization rule:** same session+worker → at most one active execution at a time. Multiple sessions run concurrently.

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
