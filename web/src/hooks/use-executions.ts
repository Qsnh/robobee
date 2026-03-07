import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api"

export function useExecutions() {
  return useQuery({
    queryKey: ["executions"],
    queryFn: api.executions.list,
  })
}

export function useExecution(id: string) {
  return useQuery({
    queryKey: ["executions", id],
    queryFn: () => api.executions.get(id),
  })
}

export function useExecutionEmails(id: string) {
  return useQuery({
    queryKey: ["executions", id, "emails"],
    queryFn: () => api.executions.emails(id),
  })
}

export function useApproveExecution() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.executions.approve(id),
    onSuccess: (_data, id) => {
      queryClient.invalidateQueries({ queryKey: ["executions", id] })
    },
  })
}

export function useRejectExecution() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ id, feedback }: { id: string; feedback: string }) =>
      api.executions.reject(id, feedback),
    onSuccess: (_data, { id }) => {
      queryClient.invalidateQueries({ queryKey: ["executions", id] })
    },
  })
}

export function useSendMessage() {
  return useMutation({
    mutationFn: ({ workerId, message, taskId }: { workerId: string; message: string; taskId?: string }) =>
      api.message.send(workerId, message, taskId),
  })
}
