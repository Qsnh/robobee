import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api"

export function useWorkers() {
  return useQuery({
    queryKey: ["workers"],
    queryFn: api.workers.list,
  })
}

export function useWorker(id: string) {
  return useQuery({
    queryKey: ["workers", id],
    queryFn: () => api.workers.get(id),
  })
}

export function useCreateWorker() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: {
      name: string
      description: string
      prompt?: string
      runtime_type: string
      cron_expression?: string
      recipients?: string[]
      schedule_enabled?: boolean
    }) => api.workers.create(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["workers"] })
    },
  })
}

export function useDeleteWorker() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.workers.delete(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["workers"] })
    },
  })
}

export function useWorkerExecutions(workerId: string) {
  return useQuery({
    queryKey: ["workers", workerId, "executions"],
    queryFn: () => api.workers.executions(workerId),
  })
}
