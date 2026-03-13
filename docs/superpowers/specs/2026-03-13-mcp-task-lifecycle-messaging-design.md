# MCP Task Lifecycle & Messaging Design

**Date:** 2026-03-13
**Status:** Approved

## Overview

Add three new MCP tools (`mark_task_success`, `mark_task_failed`, `send_message`) to transfer task lifecycle management and message delivery responsibility from the automated dispatcher pipeline to the AI (bee and workers). Delete the `msgsender` package as a result.

## Motivation

Currently, task completion and message sending are handled automatically by the dispatcher and msgsender. This design shifts control to the AI layer:

- Workers decide when a task is done and call `mark_task_success` or `mark_task_failed`
- Both workers and bee decide whether to send a message and what to say via `send_message`
- The dispatcher becomes a pure execution scheduler, no longer responsible for message delivery

## New MCP Tools

### `mark_task_success`

```json
{
  "name": "mark_task_success",
  "description": "Mark a task as successfully completed",
  "inputSchema": {
    "type": "object",
    "required": ["task_id"],
    "properties": {
      "task_id": { "type": "string", "description": "Task ID to mark as completed" }
    }
  }
}
```

- Calls `taskStore.UpdateStatus(ctx, taskID, "completed")`
- Returns `{"task_id": "...", "status": "completed"}`

### `mark_task_failed`

```json
{
  "name": "mark_task_failed",
  "description": "Mark a task as failed",
  "inputSchema": {
    "type": "object",
    "required": ["task_id"],
    "properties": {
      "task_id": { "type": "string", "description": "Task ID to mark as failed" },
      "reason":  { "type": "string", "description": "Optional failure reason" }
    }
  }
}
```

- Calls `taskStore.UpdateStatus(ctx, taskID, "failed")`
- `reason` is echoed in the response for caller/log tracing; not persisted to DB
- Returns `{"task_id": "...", "status": "failed", "reason": "..."}`

### `send_message`

```json
{
  "name": "send_message",
  "description": "Send a message to the user on the originating platform",
  "inputSchema": {
    "type": "object",
    "required": ["message_id", "content"],
    "properties": {
      "message_id": { "type": "string", "description": "ID of the originating platform message (used to resolve platform and reply context)" },
      "content":    { "type": "string", "description": "Message content to send" }
    }
  }
}
```

- Looks up the message via `messageStore.GetByID(ctx, messageID)` to obtain `platform` and `raw`
- Constructs `platform.OutboundMessage{ReplyTo: inboundMsg, Content: content}` using the stored raw data
- Looks up sender with an explicit presence check: if `stored.Platform` is not in `senders`, return an error (log + tool error, no panic)
- Calls `senders[platform].Send(ctx, outboundMsg)`
- Feishu send vs reply is determined internally by `FeishuSender` based on `chatType` (p2p → send, group → reply) — no AI input needed
- DingTalk replies via `SessionWebhook` embedded in raw data
- Returns `{"status": "sent"}`

## Caller Permissions

No fine-grained permission separation at this time (single shared API key):

| Tool | Intended caller |
|------|----------------|
| `mark_task_success` | Worker |
| `mark_task_failed` | Worker |
| `send_message` | Worker + Bee |

Both bee and worker use the same MCP API key. Business convention governs who calls what.

## MCPServer Wiring

### Struct changes

```go
type MCPServer struct {
    workerStore  *store.WorkerStore
    manager      *worker.Manager
    taskStore    *store.TaskStore
    messageStore *store.MessageStore                        // new
    senders      map[string]platform.PlatformSenderAdapter // new
    // ...
}
```

### Constructor

```go
func NewServer(
    ws      *store.WorkerStore,
    mgr     *worker.Manager,
    ts      *store.TaskStore,
    ms      *store.MessageStore,
    senders map[string]platform.PlatformSenderAdapter,
) *MCPServer
```

### app.go wiring

Currently `mcpSrv` is constructed at line 72, **before** `sendersByPlatform` is created at line 78. Move the `NewServer` call to after `sendersByPlatform := make(...)` so the map can be passed in:

```go
sendersByPlatform := make(map[string]platform.PlatformSenderAdapter)
// ... buildBee, buildPipeline ...
mcpSrv := mcp.NewServer(s.workerStore, mgr, s.taskStore, s.msgStore, sendersByPlatform)
// ... buildPlatforms, populate sendersByPlatform ...
```

`sendersByPlatform` is a map (reference type). It can be empty at `NewServer` construction time — the existing population loop fills it before any goroutines start, and MCPServer shares the same map instance.

## Store Layer Changes

### `TaskStore.UpdateStatus` (new)

```go
func (s *TaskStore) UpdateStatus(ctx context.Context, taskID, status string) error
```

Used by `mark_task_success` and `mark_task_failed`. The existing `SetExecution` requires an `executionID` and has different semantics; a dedicated method is cleaner.

### `MessageStore.GetByID` (new)

There is no `model.PlatformMessage` type. Add a store-local struct and method:

```go
// StoredMessage is the subset of platform_messages fields needed for sending.
type StoredMessage struct {
    Platform   string
    SessionKey string
    Raw        string
}

func (s *MessageStore) GetByID(ctx context.Context, id string) (StoredMessage, error)
```

Queries `platform_messages` for `platform`, `session_key`, `raw` by `id`.

### `send_message` InboundMessage reconstruction

`send_message` reconstructs a minimal `platform.InboundMessage` from the stored row to pass as `ReplyTo`. Only the fields both platform senders actually access are required:

```go
inbound := platform.InboundMessage{
    Platform:   stored.Platform,
    SessionKey: stored.SessionKey,
    Raw:        stored.Raw,
}
outbound := platform.OutboundMessage{ReplyTo: inbound, Content: content}
senders[stored.Platform].Send(ctx, outbound)
```

`FeishuSender.Send` uses `Raw` to unmarshal event data (chatId, chatType, messageId) and `Platform` is used for sender lookup. `DingTalkSender.Send` uses `Raw` to get `SessionWebhook`. All other `InboundMessage` fields (`SenderID`, `PlatformMessageID`, etc.) are zero-valued and unused by both senders.

## Dispatcher Simplification

### Removed

- `out chan msgsender.SenderEvent` field and `Out()` method
- ACK sending (`SenderEventACK`) on task start (`handleInbound`)
- Result/error sending (`SenderEventResult`, `SenderEventError`) in `handleResult`
- Clear command confirmation send (`d.out <- SenderEvent{..., Content: clearMsg}`) in `handleInbound` — **note: this message was already silently dropped** because the clear `DispatchTask` carries no `Raw` field, causing both `FeishuSender` and `DingTalkSender` to fail their unmarshal and return nil; the UX is unchanged
- `SetExecution` calls for terminal statuses in `waitForResult`: remove the `TaskStatusCompleted` and `TaskStatusFailed` writes — workers own terminal status via `mark_task_success/failed`. The `SetExecution` call that records the execution ID and sets `TaskStatusRunning` at execution start is **kept**
- `internalResult.content` field (no longer used after result sending is removed). `internalResult` becomes `{queueKey string, task DispatchTask}` only. `handleResult` no longer sends to `d.out`; it only advances the queue (same pending-task dequeue / `state.executing = false` logic as today, minus the `d.out` send at the top)
- Import of `msgsender` package

### Retained

- Per-(session, worker) queue for serialized execution ordering
- `waitForResult` polling loop — still needed to detect execution completion and advance the queue to the next pending task. It still polls until exec status is `completed` or `failed`, then sends to `d.results` to trigger queue advancement. It no longer writes terminal task status.
- Session context upsert on successful completion in `waitForResult` (this is dispatcher's responsibility, not the worker's)

### Worker instruction injection

Before calling `ExecuteWorker`, the dispatcher prepends metadata to the instruction:

```
[系统元数据] task_id={task_id} message_id={message_id}

{original instruction}
```

This gives the worker (Claude) the identifiers it needs to call `mark_task_success/failed` and `send_message`.

## msgsender Deletion

`internal/msgsender/` (including tests) is deleted entirely.

`buildPipeline` return signature changes:

```go
// Before
func buildPipeline(...) (*msgingest.Gateway, *dispatcher.Dispatcher, *msgsender.Gateway)

// After
func buildPipeline(...) (*msgingest.Gateway, *dispatcher.Dispatcher)
```

`sender.Run(ctx)` is removed from the `runners` slice in `buildApp`.

## Data Flow (after)

```
User message
  → msgingest
  → Feeder → bee (Claude) calls create_task / send_message via MCP
  → TaskScheduler → Dispatcher
  → Dispatcher injects task_id+message_id into instruction → ExecuteWorker
  → Worker (Claude) calls mark_task_success/failed + send_message via MCP
  → MCPServer → MessageStore.GetByID → senders[platform].Send → platform
```

## File Change Summary

| File | Change |
|------|--------|
| `internal/mcp/server.go` | Add `messageStore`, `senders` fields; update `NewServer` |
| `internal/mcp/tools.go` | Add 3 tool schemas + 3 tool handlers; update stale comment ("5 worker CRUD tools") and any test count assertions |
| `internal/store/task_store.go` | Add `UpdateStatus` |
| `internal/store/message_store.go` | Add `GetByID` |
| `internal/dispatcher/dispatcher.go` | Remove `out` channel, ACK/result sending; add instruction injection |
| `internal/msgsender/` | **Delete entire package** |
| `cmd/server/app.go` | Update `buildPipeline`, `buildAPIServer` calls; remove sender runner |

## Out of Scope

- Fine-grained per-caller permissions (future work)
- Removing `waitForResult` polling from dispatcher (future refactor once queue mechanism is revisited)
- Changes to bee's system prompt / CLAUDE.md to instruct it to use the new tools
