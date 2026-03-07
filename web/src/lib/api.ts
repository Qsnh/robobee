import type { Worker, Task, TaskExecution, Email } from "./types"

const API_BASE = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080/api"

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
    list: () => fetchAPI<Worker[]>("/workers"),
    get: (id: string) => fetchAPI<Worker>(`/workers/${id}`),
    create: (data: { name: string; description: string; runtime_type: string }) =>
      fetchAPI<Worker>("/workers", { method: "POST", body: JSON.stringify(data) }),
    update: (id: string, data: Partial<Worker>) =>
      fetchAPI<Worker>(`/workers/${id}`, { method: "PUT", body: JSON.stringify(data) }),
    delete: (id: string) => fetchAPI(`/workers/${id}`, { method: "DELETE" }),
  },
  tasks: {
    listByWorker: (workerId: string) => fetchAPI<Task[]>(`/workers/${workerId}/tasks`),
    create: (workerId: string, data: Omit<Task, "id" | "worker_id" | "created_at" | "updated_at">) =>
      fetchAPI<Task>(`/workers/${workerId}/tasks`, { method: "POST", body: JSON.stringify(data) }),
    update: (id: string, data: Partial<Task>) =>
      fetchAPI<Task>(`/tasks/${id}`, { method: "PUT", body: JSON.stringify(data) }),
    delete: (id: string) => fetchAPI(`/tasks/${id}`, { method: "DELETE" }),
    execute: (id: string) =>
      fetchAPI<TaskExecution>(`/tasks/${id}/execute`, { method: "POST" }),
  },
  executions: {
    list: () => fetchAPI<TaskExecution[]>("/executions"),
    get: (id: string) => fetchAPI<TaskExecution>(`/executions/${id}`),
    approve: (id: string) => fetchAPI(`/executions/${id}/approve`, { method: "POST" }),
    reject: (id: string, feedback: string) =>
      fetchAPI(`/executions/${id}/reject`, { method: "POST", body: JSON.stringify({ feedback }) }),
    emails: (id: string) => fetchAPI<Email[]>(`/executions/${id}/emails`),
  },
  message: {
    send: (workerId: string, message: string, taskId?: string) =>
      fetchAPI<TaskExecution>(`/workers/${workerId}/message`, {
        method: "POST",
        body: JSON.stringify({ message, task_id: taskId }),
      }),
  },
}
