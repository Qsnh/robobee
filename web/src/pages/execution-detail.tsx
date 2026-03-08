import { useEffect, useRef, useState } from "react"
import { useParams, Link } from "react-router-dom"
import { useExecution } from "@/hooks/use-executions"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"

const statusColor: Record<string, string> = {
  pending: "bg-gray-100 text-gray-800",
  running: "bg-blue-100 text-blue-800",
  completed: "bg-green-100 text-green-800",
  failed: "bg-red-100 text-red-800",
}

interface LogEntry {
  type: string
  content: string
}

export function ExecutionDetail() {
  const { id } = useParams<{ id: string }>()
  const { data: execution, error: fetchError } = useExecution(id!)
  const [logs, setLogs] = useState<LogEntry[]>([])
  const logsEndRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const wsBase = import.meta.env.VITE_API_URL || "http://localhost:8080/api"
    const wsUrl = wsBase.replace(/^http/, "ws") + `/executions/${id}/logs`
    const ws = new WebSocket(wsUrl)

    ws.onmessage = (event) => {
      const data = JSON.parse(event.data)
      setLogs((prev) => [...prev, data])
    }

    ws.onerror = () => {}

    return () => ws.close()
  }, [id])

  useEffect(() => {
    logsEndRef.current?.scrollIntoView({ behavior: "smooth" })
  }, [logs])

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
          <div className="bg-black text-green-400 font-mono text-sm p-4 rounded-lg max-h-[500px] overflow-y-auto">
            {logs.length === 0 && (
              <p className="text-gray-500">
                {execution.status === "running"
                  ? "Waiting for output..."
                  : "No live logs available."}
              </p>
            )}
            {logs.map((log, i) => (
              <div
                key={i}
                className={
                  log.type === "stderr"
                    ? "text-red-400"
                    : log.type === "error"
                    ? "text-red-500 font-bold"
                    : ""
                }
              >
                {log.content}
              </div>
            ))}
            <div ref={logsEndRef} />
          </div>
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
              <p><strong>Session ID:</strong> <span className="font-mono text-sm">{execution.session_id}</span></p>
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
    </div>
  )
}
