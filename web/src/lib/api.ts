import type { Worker, Task, TaskExecution, Email } from "./types"

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
    create: (data: { name: string; description: string; runtime_type: string }) =>
      fetchAPI<Worker>("/workers", { method: "POST", body: JSON.stringify(data) }),
    update: (id: string, data: Partial<Worker>) =>
      fetchAPI<Worker>(`/workers/${id}`, { method: "PUT", body: JSON.stringify(data) }),
    delete: (id: string) => fetchAPI(`/workers/${id}`, { method: "DELETE" }),
  },
  tasks: {
    listByWorker: async (workerId: string) => {
      const tasks = await fetchAPI<Task[] | null>(`/workers/${workerId}/tasks`)
      return Array.isArray(tasks) ? tasks : []
    },
    create: (workerId: string, data: Omit<Task, "id" | "worker_id" | "created_at" | "updated_at">) =>
      fetchAPI<Task>(`/workers/${workerId}/tasks`, { method: "POST", body: JSON.stringify(data) }),
    update: (id: string, data: Partial<Task>) =>
      fetchAPI<Task>(`/tasks/${id}`, { method: "PUT", body: JSON.stringify(data) }),
    delete: (id: string) => fetchAPI(`/tasks/${id}`, { method: "DELETE" }),
    execute: (id: string) =>
      fetchAPI<TaskExecution>(`/tasks/${id}/execute`, { method: "POST" }),
  },
  executions: {
    list: async () => {
      const executions = await fetchAPI<TaskExecution[] | null>("/executions")
      return Array.isArray(executions) ? executions : []
    },
    get: (id: string) => fetchAPI<TaskExecution>(`/executions/${id}`),
    approve: (id: string) => fetchAPI(`/executions/${id}/approve`, { method: "POST" }),
    reject: (id: string, feedback: string) =>
      fetchAPI(`/executions/${id}/reject`, { method: "POST", body: JSON.stringify({ feedback }) }),
    emails: async (id: string) => {
      const emails = await fetchAPI<Email[] | null>(`/executions/${id}/emails`)
      return Array.isArray(emails) ? emails : []
    },
  },
  message: {
    send: (workerId: string, message: string, taskId?: string) =>
      fetchAPI<TaskExecution>(`/workers/${workerId}/message`, {
        method: "POST",
        body: JSON.stringify({ message, task_id: taskId }),
      }),
  },
}
