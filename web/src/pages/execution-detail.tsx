import { useState } from "react"
import { useParams, Link, useNavigate } from "react-router-dom"
import { useExecution, useReplyExecution } from "@/hooks/use-executions"
import { LogViewer } from "@/components/log-viewer"
import { Card, CardContent } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"

const statusColor: Record<string, string> = {
  pending: "bg-gray-100 text-gray-800",
  running: "bg-blue-100 text-blue-800",
  completed: "bg-green-100 text-green-800",
  failed: "bg-red-100 text-red-800",
}

export function ExecutionDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: execution, error: fetchError } = useExecution(id!)
  const [replyText, setReplyText] = useState("")
  const [replyError, setReplyError] = useState<string | null>(null)
  const replyExecution = useReplyExecution()

  const handleReply = async () => {
    if (!id || !replyText.trim()) return
    setReplyError(null)
    try {
      const newExec = await replyExecution.mutateAsync({ executionId: id, message: replyText })
      navigate(`/sessions/${newExec.session_id}`)
    } catch (err) {
      setReplyError(err instanceof Error ? err.message : "Failed to send reply")
    }
  }

  if (!execution) return <p>Loading...</p>

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold">Execution Detail</h1>
          <p className="text-sm text-muted-foreground font-mono">{execution.id}</p>
        </div>
        <Badge className={statusColor[execution.status] || ""}>
          {execution.status}
        </Badge>
      </div>

      {fetchError && (
        <p className="text-red-500 mb-4">{fetchError.message}</p>
      )}

      <Tabs defaultValue="logs">
        <TabsList>
          <TabsTrigger value="logs">Logs</TabsTrigger>
          <TabsTrigger value="result">Result</TabsTrigger>
<TabsTrigger value="info">Info</TabsTrigger>
        </TabsList>

        <TabsContent value="logs" className="mt-4">
          <LogViewer
            executionId={execution.id}
            status={execution.status}
            logs={execution.logs}
          />
        </TabsContent>

        <TabsContent value="result" className="mt-4">
          <Card>
            <CardContent className="pt-6">
              <pre className="whitespace-pre-wrap text-sm">
                {execution.result || "No result yet."}
              </pre>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="info" className="mt-4">
          <Card>
            <CardContent className="pt-6 space-y-2">
              <p><strong>Worker:</strong>{" "}
                <Link to={`/workers/${execution.worker_id}`} className="font-mono text-sm hover:underline">
                  {execution.worker_id.slice(0, 8)}...
                </Link>
              </p>
              <p><strong>Session:</strong>{" "}
                <Link to={`/sessions/${execution.session_id}`} className="font-mono text-sm hover:underline">
                  {execution.session_id}
                </Link>
              </p>
              {execution.trigger_input && (
                <div>
                  <strong>Trigger Input:</strong>
                  <pre className="mt-1 whitespace-pre-wrap text-sm bg-muted p-2 rounded-md">
                    {execution.trigger_input}
                  </pre>
                </div>
              )}
              <p><strong>PID:</strong> {execution.ai_process_pid || "N/A"}</p>
              <p><strong>Started:</strong> {execution.started_at ? new Date(execution.started_at).toLocaleString() : "-"}</p>
              <p><strong>Completed:</strong> {execution.completed_at ? new Date(execution.completed_at).toLocaleString() : "-"}</p>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>

      {execution.status === "completed" && (
        <div className="mt-6">
          <h2 className="text-lg font-semibold mb-2">Reply</h2>
          {replyError && <p className="text-red-500 mb-2">{replyError}</p>}
          <Textarea
            value={replyText}
            onChange={(e) => setReplyText(e.target.value)}
            placeholder="Continue the conversation..."
            rows={4}
            className="mb-2"
          />
          <Button
            onClick={handleReply}
            disabled={replyExecution.isPending || !replyText.trim()}
          >
            {replyExecution.isPending ? "Sending..." : "Send Reply"}
          </Button>
        </div>
      )}
    </div>
  )
}
