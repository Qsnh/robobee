# Remove Worker Scheduling Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove all scheduled/cron task functionality from the Worker entity so workers become purely on-demand.

**Architecture:** Strip schedule fields from the Worker model and database via a 4-step table-rebuild migration, then delete the `internal/scheduler` package and `CronResolver` AI interface after all callers are updated. Each chunk is designed to leave the codebase in a compilable state at its end.

**Tech Stack:** Go (gin, mattn/go-sqlite3), React + TypeScript (tanstack-query), i18next for both backend (go-i18n) and frontend locales.

**Spec:** `docs/superpowers/specs/2026-03-12-remove-worker-scheduling-design.md`

---

## Chunk 1: Backend Data Layer (model + store + migrations)

> After completing all 3 tasks in this chunk, `go build ./internal/...` and `go test ./internal/store/...` should both pass. Do NOT run compilation checks between individual tasks — the model and store changes are interdependent and must be applied together before building.

### Task 1: Add database migrations to drop schedule columns

**Files:**
- Modify: `internal/store/db.go`

- [ ] **Step 1: Add 5 new single-statement migrations after version 10**

In `internal/store/db.go`, append these entries to the `migrations` slice. Each entry has exactly one SQL statement — the project's migration framework runs each via a single `tx.Exec` call and cannot handle multiple statements per entry.

```go
{
    version: 11,
    name:    "20260312000002_workers_drop_schedule_index",
    sql:     `DROP INDEX IF EXISTS idx_workers_schedule`,
},
{
    version: 12,
    name:    "20260312000003_workers_create_new",
    sql: `CREATE TABLE workers_new (
        id TEXT PRIMARY KEY,
        name TEXT NOT NULL,
        work_dir TEXT NOT NULL,
        status TEXT NOT NULL DEFAULT 'idle',
        description TEXT NOT NULL DEFAULT '',
        prompt TEXT NOT NULL DEFAULT '',
        created_at INTEGER NOT NULL,
        updated_at INTEGER NOT NULL
    )`,
},
{
    version: 13,
    name:    "20260312000004_workers_copy_data",
    sql: `INSERT INTO workers_new (id, name, work_dir, status, description, prompt, created_at, updated_at)
          SELECT id, name, work_dir, status, description, prompt, created_at, updated_at FROM workers`,
},
{
    version: 14,
    name:    "20260312000005_workers_drop_old",
    sql:     `DROP TABLE workers`,
},
{
    version: 15,
    name:    "20260312000006_workers_rename",
    sql:     `ALTER TABLE workers_new RENAME TO workers`,
},
```

Note: Migration 11 explicitly drops the `idx_workers_schedule` partial index (created in migration 6) before the table rebuild. The SELECT in migration 13 uses an explicit named column list on both sides to avoid any column order dependency.

---

### Task 2: Remove schedule fields from Worker model

**Files:**
- Modify: `internal/model/worker.go`

- [ ] **Step 1: Remove the 3 schedule fields**

In `internal/model/worker.go`, delete these 3 lines:

```go
CronExpression      string       `json:"cron_expression,omitempty" db:"cron_expression"`
ScheduleDescription string       `json:"schedule_description,omitempty" db:"schedule_description"`
ScheduleEnabled     bool         `json:"schedule_enabled" db:"schedule_enabled"`
```

The struct after the change:

```go
type Worker struct {
    ID          string       `json:"id" db:"id"`
    Name        string       `json:"name" db:"name"`
    Description string       `json:"description" db:"description"`
    Prompt      string       `json:"prompt" db:"prompt"`
    WorkDir     string       `json:"work_dir" db:"work_dir"`
    Status      WorkerStatus `json:"status" db:"status"`
    CreatedAt   int64        `json:"created_at" db:"created_at"`
    UpdatedAt   int64        `json:"updated_at" db:"updated_at"`
}
```

---

### Task 3: Update worker store to remove schedule columns

**Files:**
- Modify: `internal/store/worker_store.go`

- [ ] **Step 1: Update `workerColumns` constant**

Change:
```go
const workerColumns = `id, name, description, prompt, work_dir, cron_expression, schedule_description, schedule_enabled, status, created_at, updated_at`
```

To:
```go
const workerColumns = `id, name, description, prompt, work_dir, status, created_at, updated_at`
```

- [ ] **Step 2: Update `scanWorker`**

Change:
```go
err := scanner.Scan(
    &w.ID, &w.Name, &w.Description, &w.Prompt,
    &w.WorkDir, &w.CronExpression, &w.ScheduleDescription,
    &w.ScheduleEnabled, &w.Status, &w.CreatedAt, &w.UpdatedAt,
)
```

To:
```go
err := scanner.Scan(
    &w.ID, &w.Name, &w.Description, &w.Prompt,
    &w.WorkDir, &w.Status, &w.CreatedAt, &w.UpdatedAt,
)
```

- [ ] **Step 3: Update `Create` INSERT**

Change:
```go
_, err := s.db.Exec(
    `INSERT INTO workers (id, name, description, prompt, work_dir, cron_expression, schedule_description, schedule_enabled, status, created_at, updated_at)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
    w.ID, w.Name, w.Description, w.Prompt, w.WorkDir,
    w.CronExpression, w.ScheduleDescription, w.ScheduleEnabled,
    w.Status, w.CreatedAt, w.UpdatedAt,
)
```

To:
```go
_, err := s.db.Exec(
    `INSERT INTO workers (id, name, description, prompt, work_dir, status, created_at, updated_at)
     VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
    w.ID, w.Name, w.Description, w.Prompt, w.WorkDir,
    w.Status, w.CreatedAt, w.UpdatedAt,
)
```

- [ ] **Step 4: Update `Update` SET**

Change:
```go
_, err := s.db.Exec(
    `UPDATE workers SET name=?, description=?, prompt=?, work_dir=?,
     cron_expression=?, schedule_description=?, schedule_enabled=?, status=?, updated_at=?
     WHERE id=?`,
    w.Name, w.Description, w.Prompt, w.WorkDir,
    w.CronExpression, w.ScheduleDescription, w.ScheduleEnabled,
    w.Status, w.UpdatedAt, w.ID,
)
```

To:
```go
_, err := s.db.Exec(
    `UPDATE workers SET name=?, description=?, prompt=?, work_dir=?, status=?, updated_at=?
     WHERE id=?`,
    w.Name, w.Description, w.Prompt, w.WorkDir,
    w.Status, w.UpdatedAt, w.ID,
)
```

- [ ] **Step 5: Delete `ListScheduledWorkers` method**

Delete the entire `ListScheduledWorkers` method (the method that queries `WHERE schedule_enabled = 1`).

- [ ] **Step 6: Build the data layer packages**

```bash
go build ./internal/model/... ./internal/store/...
```

Expected: no errors.

- [ ] **Step 7: Run store tests**

```bash
go test ./internal/store/... -v -run TestWorkerStore
```

Expected: all 5 tests pass (`TestWorkerStore_Create`, `GetByID`, `List`, `Update`, `Delete`). The test DB runs through all 15 migrations on each run.

- [ ] **Step 8: Commit**

```bash
git add internal/store/db.go internal/model/worker.go internal/store/worker_store.go
git commit -m "feat(store): remove schedule columns — migrations v11-v15, model, store"
```

---

## Chunk 2: Backend Business/API Layer

> After completing all tasks in this chunk, `go build ./...` should pass. The scheduler package and CronResolver interface are deleted at the **end** of this chunk (Task 7), after all their callers have been updated.

### Task 4: Update worker manager

**Files:**
- Modify: `internal/worker/manager.go`

- [ ] **Step 1: Remove `cronResolver` from `Manager` struct and `NewManager`**

In the `Manager` struct, delete:
```go
cronResolver   ai.CronResolver
```

Change `NewManager` signature from:
```go
func NewManager(
    cfg config.Config,
    ws *store.WorkerStore,
    es *store.ExecutionStore,
    cronResolver ai.CronResolver,
) *Manager {
    return &Manager{
        cfg:            cfg,
        workerStore:    ws,
        executionStore: es,
        cronResolver:   cronResolver,
        activeRuntimes: make(map[string]Runtime),
        logSubscribers: make(map[string][]chan Output),
    }
}
```

To:
```go
func NewManager(
    cfg config.Config,
    ws *store.WorkerStore,
    es *store.ExecutionStore,
) *Manager {
    return &Manager{
        cfg:            cfg,
        workerStore:    ws,
        executionStore: es,
        activeRuntimes: make(map[string]Runtime),
        logSubscribers: make(map[string][]chan Output),
    }
}
```

- [ ] **Step 2: Delete `ResolveCron` method**

Delete:
```go
func (m *Manager) ResolveCron(ctx context.Context, description string) (string, error) {
    return m.cronResolver.CronFromDescription(ctx, description)
}
```

- [ ] **Step 3: Simplify `CreateWorker`**

Change the function signature from:
```go
func (m *Manager) CreateWorker(
    name, description, prompt string,
    scheduleDescription string,
    scheduleEnabled bool,
    workDir string,
) (model.Worker, error) {
```

To:
```go
func (m *Manager) CreateWorker(
    name, description, prompt string,
    workDir string,
) (model.Worker, error) {
```

Remove the cron resolution block entirely:
```go
var cronExpression string
if scheduleEnabled && scheduleDescription != "" {
    var err error
    cronExpression, err = m.cronResolver.CronFromDescription(context.Background(), scheduleDescription)
    if err != nil {
        return model.Worker{}, fmt.Errorf("generate cron expression: %w", err)
    }
}
```

Update the `workerStore.Create` call from:
```go
return m.workerStore.Create(model.Worker{
    ID:                  id,
    Name:                name,
    Description:         description,
    Prompt:              prompt,
    WorkDir:             workDir,
    CronExpression:      cronExpression,
    ScheduleDescription: scheduleDescription,
    ScheduleEnabled:     scheduleEnabled,
})
```

To:
```go
return m.workerStore.Create(model.Worker{
    ID:          id,
    Name:        name,
    Description: description,
    Prompt:      prompt,
    WorkDir:     workDir,
})
```

- [ ] **Step 4: Simplify `ExecuteWorker` — remove dead "scheduled" guard**

On line 133, change:
```go
if triggerInput != "" && triggerInput != "scheduled" {
```

To:
```go
if triggerInput != "" {
```

- [ ] **Step 5: Clean up unused imports in `manager.go`**

After deleting `ResolveCron`, check if the `"github.com/robobee/core/internal/ai"` import is still needed. If no remaining code in manager.go references the `ai` package, remove that import line.

- [ ] **Step 6: Build worker package**

```bash
go build ./internal/worker/...
```

Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git add internal/worker/manager.go
git commit -m "feat(worker): remove cronResolver, ResolveCron, and schedule params from manager"
```

---

### Task 5: Update API router and worker handlers

**Files:**
- Modify: `internal/api/router.go`
- Modify: `internal/api/worker_handler.go`

- [ ] **Step 1: Remove `WorkerScheduler` and `scheduler` from `router.go`**

Delete the `WorkerScheduler` interface definition:
```go
// WorkerScheduler is the minimal interface api needs from the scheduler.
type WorkerScheduler interface {
    AddWorker(workerID, cronExpr string) error
    RemoveWorker(workerID string)
}
```

Remove `scheduler WorkerScheduler` from the `Server` struct.

Change `NewServer` signature from:
```go
func NewServer(
    ws *store.WorkerStore,
    es *store.ExecutionStore,
    mgr *worker.Manager,
    sched WorkerScheduler,
) *Server {
```

To:
```go
func NewServer(
    ws *store.WorkerStore,
    es *store.ExecutionStore,
    mgr *worker.Manager,
) *Server {
```

Remove `scheduler: sched` from the `Server` initialization inside `NewServer`.

- [ ] **Step 2: Update `createWorkerRequest` in `worker_handler.go`**

Change:
```go
type createWorkerRequest struct {
    Name                string `json:"name" binding:"required"`
    Description         string `json:"description"`
    Prompt              string `json:"prompt"`
    ScheduleDescription string `json:"schedule_description"`
    ScheduleEnabled     bool   `json:"schedule_enabled"`
    WorkDir             string `json:"work_dir"`
}
```

To:
```go
type createWorkerRequest struct {
    Name        string `json:"name" binding:"required"`
    Description string `json:"description"`
    Prompt      string `json:"prompt"`
    WorkDir     string `json:"work_dir"`
}
```

- [ ] **Step 3: Update `createWorker` handler**

Remove the schedule validation block:
```go
if req.ScheduleEnabled {
    if req.ScheduleDescription == "" {
        c.JSON(http.StatusBadRequest, gin.H{"error": localize(c, "ScheduleDescriptionRequired")})
        return
    }
    if req.Prompt == "" {
        c.JSON(http.StatusBadRequest, gin.H{"error": localize(c, "PromptRequired")})
        return
    }
}
```

Change the `manager.CreateWorker` call from:
```go
w, err := s.manager.CreateWorker(
    req.Name, req.Description, req.Prompt,
    req.ScheduleDescription, req.ScheduleEnabled, req.WorkDir,
)
```

To:
```go
w, err := s.manager.CreateWorker(
    req.Name, req.Description, req.Prompt, req.WorkDir,
)
```

Remove the `s.applySchedule(w)` call.

- [ ] **Step 4: Update `updateWorker` handler**

Change the inline request struct from:
```go
var req struct {
    Name                string `json:"name"`
    Description         string `json:"description"`
    Prompt              string `json:"prompt"`
    ScheduleDescription string `json:"schedule_description"`
    ScheduleEnabled     *bool  `json:"schedule_enabled"`
}
```

To:
```go
var req struct {
    Name        string `json:"name"`
    Description string `json:"description"`
    Prompt      string `json:"prompt"`
}
```

Remove the schedule description resolution block:
```go
if req.ScheduleDescription != "" {
    cronExpression, err := s.manager.ResolveCron(c.Request.Context(), req.ScheduleDescription)
    if err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": localizeWithData(c, "FailedToGenerateCronExpression", map[string]string{"Error": err.Error()})})
        return
    }
    w.ScheduleDescription = req.ScheduleDescription
    w.CronExpression = cronExpression
}
```

Remove the schedule enabled block:
```go
if req.ScheduleEnabled != nil {
    w.ScheduleEnabled = *req.ScheduleEnabled
}
```

Remove the `s.applySchedule(updated)` call.

- [ ] **Step 5: Update `deleteWorker` — remove the `RemoveWorker` call**

Change:
```go
func (s *Server) deleteWorker(c *gin.Context) {
    id := c.Param("id")
    deleteWorkDir := c.Query("delete_work_dir") == "true"
    if err := s.manager.DeleteWorker(id, deleteWorkDir); err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    s.scheduler.RemoveWorker(id)
    c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}
```

To:
```go
func (s *Server) deleteWorker(c *gin.Context) {
    id := c.Param("id")
    deleteWorkDir := c.Query("delete_work_dir") == "true"
    if err := s.manager.DeleteWorker(id, deleteWorkDir); err != nil {
        c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
        return
    }
    c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}
```

- [ ] **Step 6: Delete `applySchedule` method**

Delete the entire `applySchedule` method:
```go
func (s *Server) applySchedule(w model.Worker) {
    if w.ScheduleEnabled && w.CronExpression != "" {
        if err := s.scheduler.AddWorker(w.ID, w.CronExpression); err != nil {
            log.Printf("failed to schedule worker %s: %v", w.ID, err)
        }
    } else {
        s.scheduler.RemoveWorker(w.ID)
    }
}
```

- [ ] **Step 7: Clean up unused imports in `worker_handler.go`**

After deleting `applySchedule`, check if `"log"` and `"github.com/robobee/core/internal/model"` are still used anywhere in the file. `"log"` was used only by `applySchedule`'s `log.Printf` — remove it. `model` was used for `model.Worker` in `applySchedule` only — remove that import too.

- [ ] **Step 8: Build API package**

```bash
go build ./internal/api/...
```

Expected: no errors.

- [ ] **Step 9: Commit**

```bash
git add internal/api/router.go internal/api/worker_handler.go
git commit -m "feat(api): remove scheduler wiring and schedule fields from worker handlers"
```

---

### Task 6: Update entry point

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Remove scheduler from `main.go`**

Remove the import:
```go
"github.com/robobee/core/internal/scheduler"
```

Remove these lines:
```go
// Start cron scheduler
sched := scheduler.New(workerStore, mgr)
if err := sched.Start(); err != nil {
    log.Printf("scheduler start error: %v", err)
}
```

Change `NewManager` call from:
```go
mgr := worker.NewManager(cfg, workerStore, execStore, aiClient)
```

To:
```go
mgr := worker.NewManager(cfg, workerStore, execStore)
```

Change `NewServer` call from:
```go
srv := api.NewServer(workerStore, execStore, mgr, sched)
```

To:
```go
srv := api.NewServer(workerStore, execStore, mgr)
```

Remove `sched.Stop()` from the shutdown handler goroutine.

Update the comment from:
```go
// Initialize Claude Code client for routing and cron
```

To:
```go
// Initialize Claude Code client for routing
```

- [ ] **Step 2: Build the full project**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat(main): remove scheduler init and wire-up from main"
```

---

### Task 7: Delete scheduler package and CronResolver, run go mod tidy

**Files:**
- Delete: `internal/scheduler/cron.go`
- Modify: `internal/ai/interfaces.go`
- Modify: `internal/ai/claude_code_client.go`

> This task is done last in Chunk 2 — all callers have already been updated above, so deleting these now will not break anything.

- [ ] **Step 1: Delete the scheduler package**

```bash
rm internal/scheduler/cron.go
```

- [ ] **Step 2: Remove `CronResolver` from `internal/ai/interfaces.go`**

Delete the entire CronResolver interface block:
```go
// CronResolver converts a natural language schedule description to a cron expression.
type CronResolver interface {
    CronFromDescription(ctx context.Context, description string) (string, error)
}
```

If `"context"` is now the only import and it was only used by `CronResolver`, remove it. Check: `WorkerRouter.RouteToWorker` also takes `context.Context`, so the import stays.

- [ ] **Step 3: Remove `CronFromDescription` from `internal/ai/claude_code_client.go`**

Delete the `CronFromDescription` method and its doc comment (lines 95-110):
```go
// CronFromDescription implements CronResolver.
func (c *ClaudeCodeClient) CronFromDescription(ctx context.Context, description string) (string, error) {
    ...
}
```

Update the struct comment on line 18 from:
```go
// It implements WorkerRouter and CronResolver.
```

To:
```go
// It implements WorkerRouter.
```

- [ ] **Step 4: Build**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 5: Run go mod tidy**

```bash
go mod tidy
```

Expected: `go.mod` and `go.sum` no longer reference `github.com/robfig/cron/v3`.

Verify:
```bash
grep "robfig" go.mod
```

Expected: no output.

- [ ] **Step 6: Run all Go tests**

```bash
go test ./...
```

Expected: all tests pass.

- [ ] **Step 7: Update backend locale files**

Replace `internal/api/locales/en.json` with:
```json
{
  "WorkerNotFound": {
    "other": "worker not found"
  },
  "ExecutionNotFound": {
    "other": "execution not found"
  }
}
```

Replace `internal/api/locales/zh.json` with:
```json
{
  "WorkerNotFound": {
    "other": "未找到工作者"
  },
  "ExecutionNotFound": {
    "other": "未找到执行记录"
  }
}
```

- [ ] **Step 8: Commit**

```bash
git add -A internal/scheduler/ internal/ai/ internal/api/locales/ go.mod go.sum
git commit -m "feat: delete scheduler package and CronResolver, run go mod tidy, clean locales"
```

---

## Chunk 3: Frontend

### Task 8: Update TypeScript types, API client, and hooks

**Files:**
- Modify: `web/src/lib/types.ts`
- Modify: `web/src/lib/api.ts`
- Modify: `web/src/hooks/use-workers.ts`

- [ ] **Step 1: Remove schedule fields from `Worker` interface in `types.ts`**

Change:
```typescript
export interface Worker {
  id: string
  name: string
  description: string
  prompt: string
  work_dir: string
  cron_expression: string
  schedule_description?: string
  schedule_enabled: boolean
  status: WorkerStatus
  created_at: number
  updated_at: number
}
```

To:
```typescript
export interface Worker {
  id: string
  name: string
  description: string
  prompt: string
  work_dir: string
  status: WorkerStatus
  created_at: number
  updated_at: number
}
```

- [ ] **Step 2: Remove schedule fields from `api.ts` create type**

Change the `workers.create` function data parameter from:
```typescript
create: (data: {
  name: string
  description: string
  prompt?: string
  schedule_description?: string
  schedule_enabled?: boolean
  work_dir?: string
}) => fetchAPI<Worker>("/workers", { method: "POST", body: JSON.stringify(data) }),
```

To:
```typescript
create: (data: {
  name: string
  description: string
  prompt?: string
  work_dir?: string
}) => fetchAPI<Worker>("/workers", { method: "POST", body: JSON.stringify(data) }),
```

- [ ] **Step 3: Remove schedule fields from `useCreateWorker` in `use-workers.ts`**

Change the mutation function type from:
```typescript
mutationFn: (data: {
  name: string
  description: string
  prompt?: string
  schedule_description?: string
  schedule_enabled?: boolean
  work_dir?: string
}) => api.workers.create(data),
```

To:
```typescript
mutationFn: (data: {
  name: string
  description: string
  prompt?: string
  work_dir?: string
}) => api.workers.create(data),
```

- [ ] **Step 4: TypeScript check**

```bash
cd web && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/types.ts web/src/lib/api.ts web/src/hooks/use-workers.ts
git commit -m "feat(frontend): remove schedule fields from Worker type, API client, and hooks"
```

---

### Task 9: Update frontend pages and locales

**Files:**
- Modify: `web/src/pages/workers.tsx`
- Modify: `web/src/pages/worker-detail.tsx`
- Modify: `web/src/pages/dashboard.tsx`
- Modify: `web/src/locales/en.json`
- Modify: `web/src/locales/zh.json`

- [ ] **Step 1: Update `workers.tsx` — remove schedule state, form UI, and card display**

Remove state declarations:
```typescript
const [scheduleEnabled, setScheduleEnabled] = useState(false)
const [scheduleDescription, setScheduleDescription] = useState("")
```

Update `handleCreate` from:
```typescript
await createWorker.mutateAsync({
  name,
  description,
  prompt: prompt || undefined,
  schedule_enabled: scheduleEnabled || undefined,
  schedule_description: scheduleEnabled ? scheduleDescription : undefined,
  work_dir: workDir || undefined,
})
```

To:
```typescript
await createWorker.mutateAsync({
  name,
  description,
  prompt: prompt || undefined,
  work_dir: workDir || undefined,
})
```

Remove `setScheduleEnabled(false)` and `setScheduleDescription("")` from the post-create reset.

In the create form, remove the schedule checkbox div:
```tsx
<div className="flex items-center gap-2">
  <input type="checkbox" id="schedule" checked={scheduleEnabled}
    onChange={(e) => setScheduleEnabled(e.target.checked)} />
  <Label htmlFor="schedule">{t("workers.form.enableSchedule")}</Label>
</div>
```

Remove the entire `{scheduleEnabled && (...)}` conditional block. **Important:** Move the prompt textarea out of this conditional block to always be visible in the form, placed after the work dir field:

```tsx
<div>
  <Label htmlFor="prompt">{t("workers.form.prompt")}</Label>
  <Textarea
    id="prompt"
    value={prompt}
    onChange={(e) => setPrompt(e.target.value)}
    placeholder={t("workers.form.promptPlaceholder")}
    rows={4}
  />
</div>
```

Update the worker card's schedule display from:
```tsx
<p className="text-xs text-muted-foreground">
  {w.schedule_enabled
    ? `${t("common.schedule")}: ${w.schedule_description || w.cron_expression}`
    : t("common.onDemand")}
</p>
```

To:
```tsx
<p className="text-xs text-muted-foreground">
  {t("common.onDemand")}
</p>
```

- [ ] **Step 2: Update `worker-detail.tsx` — remove schedule row from Info tab**

Remove the schedule `<p>` block:
```tsx
<p>
  <strong>{t("common.schedule")}:</strong>{" "}
  {worker.schedule_enabled
    ? t("workerDetail.scheduleEnabled", { expr: worker.cron_expression })
    : t("workerDetail.scheduleDisabled")}
</p>
```

- [ ] **Step 3: Update `dashboard.tsx` — remove schedule display**

Change:
```tsx
<p className="text-xs text-muted-foreground">
  {w.schedule_enabled
    ? `${t("common.schedule")}: ${w.cron_expression}`
    : t("common.onDemand")}
</p>
```

To:
```tsx
<p className="text-xs text-muted-foreground">
  {t("common.onDemand")}
</p>
```

- [ ] **Step 4: Update `web/src/locales/en.json` — remove orphaned keys**

Remove `"schedule": "Schedule"` from the `common` section.

Remove from `workers.form`:
- `"enableSchedule": "Enable Schedule"`
- `"scheduleDescription": "Schedule Description"`
- `"scheduleDescriptionPlaceholder": "e.g. Run at 3 AM every day"`

Update `workers.form.promptPlaceholder`:

Change:
```json
"promptPlaceholder": "The instruction this worker will execute on schedule..."
```

To:
```json
"promptPlaceholder": "The instruction this worker will execute..."
```

Remove from `workerDetail`:
- `"scheduleDisabled": "Disabled"`
- `"scheduleEnabled": "Enabled ({{expr}})"`

- [ ] **Step 5: Apply the same changes to `web/src/locales/zh.json`**

Remove `common.schedule`, `workers.form.enableSchedule`, `workers.form.scheduleDescription`, `workers.form.scheduleDescriptionPlaceholder` from zh.json.

Update `workers.form.promptPlaceholder` (remove "定时" / "on schedule" reference).

Remove `workerDetail.scheduleDisabled` and `workerDetail.scheduleEnabled`.

- [ ] **Step 6: TypeScript check**

```bash
cd web && npx tsc --noEmit
```

Expected: no errors.

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/workers.tsx web/src/pages/worker-detail.tsx web/src/pages/dashboard.tsx web/src/locales/
git commit -m "feat(frontend): remove schedule UI from workers, worker-detail, and dashboard pages"
```

---

### Task 10: Final verification

- [ ] **Step 1: Full Go build and test**

```bash
go build ./...
go test ./...
```

Expected: clean build, all tests pass.

- [ ] **Step 2: Confirm robfig/cron removed from dependencies**

```bash
grep "robfig" go.mod
```

Expected: no output.

- [ ] **Step 3: Confirm no schedule references in Go source**

```bash
grep -r "CronExpression\|ScheduleDescription\|ScheduleEnabled\|CronResolver\|CronFromDescription\|ListScheduledWorkers\|applySchedule\|cronResolver\|ResolveCron\|robfig" internal/ cmd/
```

Expected: no matches.

- [ ] **Step 4: Confirm no schedule references in TypeScript source**

```bash
grep -r "cron_expression\|schedule_description\|schedule_enabled\|scheduleEnabled\|scheduleDescription\|CronResolver\|scheduleDisabled\|common\.schedule" web/src/
```

Expected: no matches.
