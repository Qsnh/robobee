import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api"
import type { Task } from "@/lib/types"

export function useTasks(workerId: string) {
  return useQuery({
    queryKey: ["tasks", workerId],
    queryFn: async () => (await api.tasks.listByWorker(workerId)) ?? [],
  })
}

export function useCreateTask(workerId: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: Omit<Task, "id" | "worker_id" | "created_at" | "updated_at">) =>
      api.tasks.create(workerId, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["tasks", workerId] })
    },
  })
}

export function useDeleteTask(workerId: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.tasks.delete(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["tasks", workerId] })
    },
  })
}

export function useExecuteTask() {
  return useMutation({
    mutationFn: (id: string) => api.tasks.execute(id),
  })
}
