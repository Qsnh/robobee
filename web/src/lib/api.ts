import type { Worker, WorkerExecution, Email } from "./types"

const API_BASE = import.meta.env.VITE_API_URL || "http://localhost:8080/api"

async function fetchAPI<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    headers: { "Content-Type": "application/json" },
    ...options,
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  return res.json()
}

export const api = {
  workers: {
    list: async () => {
      const workers = await fetchAPI<Worker[] | null>("/workers")
      return Array.isArray(workers) ? workers : []
    },
    get: (id: string) => fetchAPI<Worker>(`/workers/${id}`),
    create: (data: {
      name: string
      description: string
      prompt: string
      runtime_type: string
      trigger_type: string
      cron_expression?: string
      recipients?: string[]
      requires_approval?: boolean
    }) => fetchAPI<Worker>("/workers", { method: "POST", body: JSON.stringify(data) }),
    update: (id: string, data: Partial<Worker>) =>
      fetchAPI<Worker>(`/workers/${id}`, { method: "PUT", body: JSON.stringify(data) }),
    delete: (id: string) => fetchAPI(`/workers/${id}`, { method: "DELETE" }),
    executions: async (id: string) => {
      const execs = await fetchAPI<WorkerExecution[] | null>(`/workers/${id}/executions`)
      return Array.isArray(execs) ? execs : []
    },
  },
  executions: {
    list: async () => {
      const executions = await fetchAPI<WorkerExecution[] | null>("/executions")
      return Array.isArray(executions) ? executions : []
    },
    get: (id: string) => fetchAPI<WorkerExecution>(`/executions/${id}`),
    approve: (id: string) => fetchAPI(`/executions/${id}/approve`, { method: "POST" }),
    reject: (id: string, feedback: string) =>
      fetchAPI(`/executions/${id}/reject`, { method: "POST", body: JSON.stringify({ feedback }) }),
    emails: async (id: string) => {
      const emails = await fetchAPI<Email[] | null>(`/executions/${id}/emails`)
      return Array.isArray(emails) ? emails : []
    },
  },
  message: {
    send: (workerId: string, message: string) =>
      fetchAPI<WorkerExecution>(`/workers/${workerId}/message`, {
        method: "POST",
        body: JSON.stringify({ message }),
      }),
  },
}
