# .robobee.claude.md 系统规则注入设计

## 背景

Bee 和 Worker 的 CLAUDE.md 管理存在两个问题：

1. **Bee**：`WriteCLAUDEMD()` 每次 feeder tick 都用 `persona + clearInstructions` 覆盖 CLAUDE.md，用户手动修改会丢失
2. **Worker**：CLAUDE.md 在 `CreateWorker()` 时创建一次后不再更新，系统新增规则无法注入已有 worker

## 方案

将系统规则拆分到 `.robobee.claude.md` 文件中，CLAUDE.md 通过 Claude Code 原生 `@path` 语法引入。每次启动/执行时只更新 `.robobee.claude.md`，并检查 CLAUDE.md 是否包含引用（缺失则追加）。

## 设计

### 1. `.robobee.claude.md` 内容

系统规则分为共享规则和角色专有规则，按角色组合后写入 `.robobee.claude.md`。

**共享规则（bee + worker）：**

```markdown
## 任务通知规范

你在执行任何任务时，必须通过 `send_message` 工具与用户保持同步。这是强制要求，不可省略。

### 何时通知

1. **任务开始时** — 收到任务后、开始实际处理之前，立即调用 `send_message` 告知用户你已接收任务并即将开始处理
2. **阶段性进展时** — 如果任务涉及多个步骤或阶段，每完成一个阶段调用 `send_message` 汇报当前进度和下一步计划
3. **任务完成时** — 任务执行完毕后，调用 `send_message` 汇报最终结果

### 通知原则

- 简洁明了，不要冗长描述
- 包含关键信息：正在做什么、完成了什么、结果是什么
- 遇到异常或阻塞时也必须通知用户
```

**Bee 专有规则：** 当前的 `clearInstructions` 常量内容（清除上下文处理）。

**Worker 专有规则：**

```markdown
## 任务状态标记

任务执行结束后，你必须根据执行结果标记任务状态：

- **成功** — 调用 `mark_task_success` 并附上结果摘要
- **失败** — 调用 `mark_task_failed` 并附上失败原因

这是每个任务的最后一步，不可遗漏。先调用 `send_message` 通知结果，再标记状态。
```

### 2. 新增 `EnsureSystemRules` 函数

新增包 `internal/claudemd`，提供通用函数：

```go
package claudemd

const (
    RoleBee    = "bee"
    RoleWorker = "worker"
    systemRulesFile = ".robobee.claude.md"
    importLine      = "@.robobee.claude.md"
)

// EnsureSystemRules writes .robobee.claude.md with the latest system rules
// for the given role, and ensures CLAUDE.md contains the @import reference.
// It does NOT create CLAUDE.md if it doesn't exist.
func EnsureSystemRules(workDir, role string) error
```

逻辑：

1. 根据 `role` 选择内容（共享规则 + 角色专有规则）
2. 写入 `{workDir}/.robobee.claude.md`（每次覆盖，确保最新）
3. 读取 `{workDir}/CLAUDE.md`：
   - 文件不存在 → 不处理（由调用方的创建逻辑负责）
   - 文件存在但不包含 `@.robobee.claude.md` → 在末尾追加 `\n@.robobee.claude.md\n`
   - 文件已包含引用 → 不修改

### 3. Bee 侧改动

**`bee_process.go`：**

- `WriteCLAUDEMD` 改为：仅在 CLAUDE.md **不存在**时创建初始内容（`persona` 部分 + `@.robobee.claude.md` 引用）
- 移除 `clearInstructions` 常量（迁移到 `claudemd` 包的 bee 规则中）

**`feeder.go`：**

- `tick()` 中原来调用 `WriteCLAUDEMD` 的地方改为：
  1. 调用 `WriteCLAUDEMD`（现在是幂等的，不会覆盖已有文件）
  2. 调用 `claudemd.EnsureSystemRules(workDir, claudemd.RoleBee)`

### 4. Worker 侧改动

**`manager.go`：**

- `CreateWorker()`：创建 CLAUDE.md 时初始内容加上 `@.robobee.claude.md` 引用，然后调用 `claudemd.EnsureSystemRules(workDir, claudemd.RoleWorker)`
- `ExecuteWorker()`、`ExecuteWorkerWithSession()` 和 `ReplyExecution()`：执行前调用 `claudemd.EnsureSystemRules(worker.WorkDir, claudemd.RoleWorker)`，确保已有 worker 也能获取最新规则

### 5. 文件结构

```
~/.robobee/
├── bee/
│   ├── CLAUDE.md              ← 用户可编辑，包含 @.robobee.claude.md
│   └── .robobee.claude.md     ← 系统管理，每次覆盖
└── worker/
    └── {worker_id}/
        ├── CLAUDE.md           ← 用户可编辑，包含 @.robobee.claude.md
        └── .robobee.claude.md  ← 系统管理，每次覆盖
```

### 6. 错误处理

`EnsureSystemRules` 失败视为**非致命错误**（log and continue）。原因：`.robobee.claude.md` 可能已存在上一次写入的版本，缺少最新规则不影响基本功能。不触发 rollback。

### 7. 内容组合顺序

`.robobee.claude.md` 内容按以下顺序组合：共享规则在前，角色专有规则在后。

### 8. 新增文件

- `internal/claudemd/claudemd.go` — `EnsureSystemRules` 函数 + 规则常量

### 9. 修改文件

- `internal/bee/bee_process.go` — `WriteCLAUDEMD` 改为幂等写入，移除 `clearInstructions`
- `internal/bee/feeder.go` — tick 中增加 `EnsureSystemRules` 调用
- `internal/worker/manager.go` — `CreateWorker`/`ExecuteWorker`/`ExecuteWorkerWithSession`/`ReplyExecution` 中增加 `EnsureSystemRules` 调用
