export type RuntimeType = "claude_code" | "codex"
export type WorkerStatus = "idle" | "working" | "error"
export type TriggerType = "manual" | "email" | "cron"
export type ExecutionStatus = "pending" | "running" | "awaiting_approval" | "approved" | "rejected" | "completed" | "failed"

export interface Worker {
  id: string
  name: string
  description: string
  email: string
  runtime_type: RuntimeType
  work_dir: string
  status: WorkerStatus
  created_at: string
  updated_at: string
}

export interface Task {
  id: string
  worker_id: string
  name: string
  plan: string
  trigger_type: TriggerType
  cron_expression: string
  recipients: string[]
  requires_approval: boolean
  created_at: string
  updated_at: string
}

export interface TaskExecution {
  id: string
  task_id: string
  session_id: string
  status: ExecutionStatus
  result: string
  ai_process_pid: number
  started_at: string | null
  completed_at: string | null
}

export interface Email {
  id: string
  execution_id: string
  from_addr: string
  to_addr: string
  cc_addr: string
  subject: string
  body: string
  in_reply_to: string
  direction: "inbound" | "outbound"
  created_at: string
}
