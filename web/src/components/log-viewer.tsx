import { useEffect, useRef, useState } from "react"

interface LogEntry {
  type: string
  content: string
}

interface LogViewerProps {
  executionId: string
  status: string
  logs: string | null | undefined
}

export function LogViewer({ executionId, status, logs }: LogViewerProps) {
  const [logEntries, setLogEntries] = useState<LogEntry[]>([])
  const logsEndRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const wsBase = import.meta.env.VITE_API_URL || "http://localhost:8080/api"
    const wsUrl = wsBase.replace(/^http/, "ws") + `/executions/${executionId}/logs`
    const ws = new WebSocket(wsUrl)

    ws.onmessage = (event) => {
      const data = JSON.parse(event.data)
      setLogEntries((prev) => [...prev, data])
    }

    ws.onerror = () => {}

    return () => ws.close()
  }, [executionId])

  useEffect(() => {
    if (status !== "running" && logs) {
      setLogEntries(
        logs.split("\n").filter(Boolean).map((line) => ({
          type: "stdout",
          content: line,
        }))
      )
    }
  }, [status, logs])

  useEffect(() => {
    logsEndRef.current?.scrollIntoView({ behavior: "smooth" })
  }, [logEntries])

  return (
    <div className="bg-black text-green-400 font-mono text-sm p-4 rounded-lg max-h-[500px] overflow-y-auto">
      {logEntries.length === 0 && (
        <p className="text-gray-500">
          {status === "running" ? "Waiting for output..." : "No logs recorded."}
        </p>
      )}
      {logEntries.map((log, i) => (
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
  )
}
