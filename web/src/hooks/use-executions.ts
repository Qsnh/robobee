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
    refetchInterval: (query) => {
      const status = query.state.data?.status
      if (status === "completed" || status === "failed") return false
      return 3000
    },
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

