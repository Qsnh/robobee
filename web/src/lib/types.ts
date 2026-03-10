export type WorkerStatus = "idle" | "working" | "error"
export type ExecutionStatus = "pending" | "running" | "completed" | "failed"

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
  logs: string | null
  ai_process_pid: number
  started_at: string | null
  completed_at: string | null
}
