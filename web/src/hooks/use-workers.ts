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
    mutationFn: (data: { name: string; description: string; runtime_type: string }) =>
      api.workers.create(data),
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
