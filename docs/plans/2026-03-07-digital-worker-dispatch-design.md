# Digital Worker Dispatch System - Design Document

**Date**: 2026-03-07
**Project**: RoboBee Core
**Status**: Approved

## Overview

A digital worker dispatch system that creates AI-powered workers (backed by Claude Code / Codex CLI instances) to execute tasks, communicate via an embedded SMTP email service, and support approval workflows through email replies.

## Key Decisions

- **Architecture**: Monolithic Go service (REST API + SMTP + Scheduler + Process Manager)
- **AI Runtime**: Support both Claude Code CLI and Codex CLI, configurable per worker
- **Email**: Embedded Go SMTP server using `go-smtp`, domain `robobee.local`
- **Approval**: Single-level approval via email reply
- **Scale**: 5-10 concurrent workers, single machine deployment
- **Tech Stack**: Go + SQLite (backend), Next.js + shadcn/ui (frontend)

## Data Model

### Workers (Digital Employees)

| Field | Type | Description |
|-------|------|-------------|
| id | UUID | Primary key |
| name | TEXT | Worker name |
| description | TEXT | Role/function description |
| email | TEXT | Auto-generated, e.g. `xiaoming@robobee.local` |
| runtime_type | TEXT | `claude_code` or `codex` |
| work_dir | TEXT | Worker's working directory path |
| status | TEXT | `idle`, `working`, `error` |
| created_at | DATETIME | |
| updated_at | DATETIME | |

### Tasks (Work Tasks)

| Field | Type | Description |
|-------|------|-------------|
| id | UUID | Primary key |
| worker_id | UUID | FK to Workers |
| name | TEXT | Task name |
| plan | TEXT | Work plan content (instructions for AI) |
| trigger_type | TEXT | `manual`, `email`, `cron` |
| cron_expression | TEXT | Optional, for cron triggers |
| recipients | JSON | List of recipient email addresses |
| requires_approval | BOOLEAN | Whether task needs approval |
| created_at | DATETIME | |
| updated_at | DATETIME | |

### TaskExecutions (Execution Records)

| Field | Type | Description |
|-------|------|-------------|
| id | UUID | Primary key |
| task_id | UUID | FK to Tasks |
| session_id | TEXT | Global session identifier for approval flow |
| status | TEXT | `pending`, `running`, `awaiting_approval`, `approved`, `rejected`, `completed`, `failed` |
| result | TEXT | Execution result/report |
| ai_process_pid | INTEGER | Running AI process PID |
| started_at | DATETIME | |
| completed_at | DATETIME | |

### Emails (Mail Records)

| Field | Type | Description |
|-------|------|-------------|
| id | UUID | Primary key |
| execution_id | UUID | FK to TaskExecutions |
| from_addr | TEXT | Sender address |
| to_addr | TEXT | Recipient address |
| cc_addr | TEXT | CC address |
| subject | TEXT | |
| body | TEXT | |
| in_reply_to | TEXT | For reply chain linking |
| direction | TEXT | `inbound` or `outbound` |
| created_at | DATETIME | |

### WorkerMemories (Persistent Memory)

| Field | Type | Description |
|-------|------|-------------|
| id | UUID | Primary key |
| worker_id | UUID | FK to Workers |
| execution_id | UUID | FK to TaskExecutions |
| summary | TEXT | Summary content |
| created_at | DATETIME | |

## System Architecture

### Project Structure

```
robobee/core/
├── cmd/
│   └── server/main.go              # Entry point
├── internal/
│   ├── api/                         # REST API handlers
│   │   ├── router.go
│   │   ├── worker_handler.go
│   │   ├── task_handler.go
│   │   └── execution_handler.go
│   ├── model/                       # Data models
│   ├── store/                       # SQLite data access (Repository pattern)
│   ├── worker/                      # Worker management & AI process management
│   │   ├── manager.go               # Worker lifecycle
│   │   └── runtime.go               # Claude Code / Codex process wrapper
│   ├── mail/                        # Embedded SMTP service
│   │   ├── server.go                # SMTP server
│   │   ├── handler.go               # Inbound email handling (approval replies)
│   │   └── sender.go                # Outbound emails (reports/approval requests)
│   ├── scheduler/                   # Cron task scheduling
│   │   └── cron.go
│   └── config/                      # Configuration
├── data/
│   ├── robobee.db                   # SQLite database
│   └── workers/                     # Worker directories
│       ├── <worker-uuid>/
│       │   ├── CLAUDE.md            # Worker memory/context
│       │   └── ...                  # Work outputs
├── web/                             # Next.js frontend
│   ├── app/
│   │   ├── page.tsx                 # Dashboard
│   │   ├── workers/                 # Worker management
│   │   ├── tasks/                   # Task management
│   │   └── executions/              # Execution records / approval
│   └── components/
├── docker-compose.yml
└── Dockerfile
```

## Core Flows

### 1. Task Trigger Flow

```
Trigger (message / email / cron)
  → Create TaskExecution (status=pending)
  → Generate session_id
  → Start AI process (claude code / codex)
     Execute in worker's work_dir
     Pass task.plan as instructions
  → Monitor process output (stream to WebSocket)
  → Process completes → Collect result
```

### 2. Report & Approval Flow

```
Task completes
  → Generate report email
  → Send to task.recipients
  → CC to system designated mailbox

If requires_approval:
  → execution.status = awaiting_approval
  → Wait for recipient email reply
  → SMTP receives reply → Parse In-Reply-To → Match session_id
  → Reply contains "approve/通过" → status = approved → Continue
  → Reply contains "reject/驳回" + feedback → status = rejected
     → Worker summarizes feedback → Persist to memory → Re-execute
```

### 3. Memory Persistence Flow

```
Task completes / Approval feedback received
  → Call AI to summarize work
  → Save to WorkerMemories table
  → Append to work_dir/CLAUDE.md (for next AI execution to read)
```

## SMTP Service

- **Library**: `github.com/emersion/go-smtp`
- **Port**: 2525 (configurable)
- **Domain**: `robobee.local` (configurable)
- **Worker email format**: `{name}@robobee.local`
- **Inbound**: Parse `In-Reply-To` header → match `session_id` → trigger approval/feedback
- **Outbound**: Send reports/approval requests → auto CC to `system_cc` address

## REST API

```
# Worker Management
POST   /api/workers              Create worker
GET    /api/workers              List workers
GET    /api/workers/:id          Get worker details
PUT    /api/workers/:id          Update worker
DELETE /api/workers/:id          Delete worker

# Task Management
POST   /api/workers/:id/tasks    Create task
GET    /api/workers/:id/tasks    List worker's tasks
PUT    /api/tasks/:id            Update task
DELETE /api/tasks/:id            Delete task

# Task Execution
POST   /api/tasks/:id/execute    Manually trigger execution
GET    /api/executions           List executions
GET    /api/executions/:id       Get execution details
POST   /api/executions/:id/approve  Approve via API
POST   /api/executions/:id/reject   Reject via API

# Message Trigger
POST   /api/workers/:id/message  Send message to trigger work

# Email Records
GET    /api/executions/:id/emails  View emails for an execution

# Real-time Logs (WebSocket)
WS     /api/executions/:id/logs   Stream AI execution output
```

## Frontend Pages

| Page | Features |
|------|----------|
| Dashboard | Worker status overview, running tasks |
| Worker Management | CRUD workers, view worker memories |
| Task Management | Configure tasks, triggers, recipients |
| Execution Detail | Real-time logs, results, approval actions |
| Email View | View email correspondence |

## AI Runtime

### Interface

```go
type Runtime interface {
    Execute(ctx context.Context, workDir string, plan string) (<-chan Output, error)
    Stop() error
}
```

### Claude Code Implementation

```
claude --dangerously-skip-permissions -p "<plan>" --output-format stream-json
```

### Codex Implementation

```
codex --quiet --approval-mode full-auto "<plan>"
```

### Process Lifecycle

- Start → Monitor stdout/stderr → Stream to WebSocket → Process exits → Collect result
- Abnormal exit → Log error → Mark execution as failed → Notify recipients
- Configurable timeout per runtime

## Configuration

```yaml
server:
  port: 8080
  host: 0.0.0.0

smtp:
  port: 2525
  domain: robobee.local
  system_cc: admin@company.com

database:
  path: ./data/robobee.db

workers:
  base_dir: ./data/workers
  default_runtime: claude_code

runtime:
  claude_code:
    binary: claude
    timeout: 30m
  codex:
    binary: codex
    timeout: 30m
```

## Go Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/gin-gonic/gin` | HTTP framework |
| `github.com/gorilla/websocket` | WebSocket for real-time logs |
| `github.com/emersion/go-smtp` | Embedded SMTP server |
| `github.com/emersion/go-message` | Email parsing |
| `github.com/robfig/cron/v3` | Cron scheduling |
| `github.com/mattn/go-sqlite3` | SQLite driver |
| `github.com/google/uuid` | UUID generation |

## Deployment

Single machine via Docker Compose:

```yaml
services:
  backend:
    build: .
    ports:
      - "8080:8080"   # HTTP API
      - "2525:2525"   # SMTP
    volumes:
      - ./data:/app/data
  frontend:
    build: ./web
    ports:
      - "3000:3000"
```
