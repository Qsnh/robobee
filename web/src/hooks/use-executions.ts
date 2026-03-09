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

export function useSendMessage() {
  return useMutation({
    mutationFn: ({ workerId, message }: { workerId: string; message: string }) =>
      api.message.send(workerId, message),
  })
}

export function useSessionExecutions(sessionId: string) {
  return useQuery({
    queryKey: ["sessions", sessionId, "executions"],
    queryFn: () => api.sessions.executions(sessionId),
    enabled: !!sessionId,
  })
}

export function useReplyExecution() {
  return useMutation({
    mutationFn: ({ executionId, message }: { executionId: string; message: string }) =>
      api.executions.reply(executionId, message),
  })
}
