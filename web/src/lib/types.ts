export type RuntimeType = "claude_code" | "codex"
export type WorkerStatus = "idle" | "working" | "error"
export type TriggerType = "cron" | "message"
export type ExecutionStatus = "pending" | "running" | "awaiting_approval" | "approved" | "rejected" | "completed" | "failed"

export interface Worker {
  id: string
  name: string
  description: string
  prompt: string
  email: string
  runtime_type: RuntimeType
  work_dir: string
  trigger_type: TriggerType
  cron_expression: string
  recipients: string[]
  requires_approval: boolean
  status: WorkerStatus
  created_at: string
  updated_at: string
}

export interface WorkerExecution {
  id: string
  worker_id: string
  session_id: string
  trigger_input: string
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
