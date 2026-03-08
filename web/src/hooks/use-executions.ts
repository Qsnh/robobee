import { useQuery, useMutation } from "@tanstack/react-query"
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

export function useSendMessage() {
  return useMutation({
    mutationFn: ({ workerId, message }: { workerId: string; message: string }) =>
      api.message.send(workerId, message),
  })
}
