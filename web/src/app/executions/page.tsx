"use client"

import { useEffect, useState } from "react"
import Link from "next/link"
import { api } from "@/lib/api"
import type { TaskExecution } from "@/lib/types"
import { Badge } from "@/components/ui/badge"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

const statusColor: Record<string, string> = {
  pending: "bg-gray-100 text-gray-800",
  running: "bg-blue-100 text-blue-800",
  awaiting_approval: "bg-yellow-100 text-yellow-800",
  approved: "bg-green-100 text-green-800",
  rejected: "bg-red-100 text-red-800",
  completed: "bg-green-100 text-green-800",
  failed: "bg-red-100 text-red-800",
}

export default function ExecutionsPage() {
  const [executions, setExecutions] = useState<TaskExecution[]>([])
  const [error, setError] = useState("")

  useEffect(() => {
    api.executions.list().then(setExecutions).catch((e) => setError(e.message))
  }, [])

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">Executions</h1>
      {error && <p className="text-red-500 mb-4">{error}</p>}

      {executions.length === 0 && !error && (
        <p className="text-muted-foreground">No executions yet.</p>
      )}

      {executions.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>Task ID</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Started</TableHead>
              <TableHead>Completed</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {executions.map((e) => (
              <TableRow key={e.id}>
                <TableCell>
                  <Link
                    href={`/executions/${e.id}`}
                    className="font-mono text-sm hover:underline"
                  >
                    {e.id.slice(0, 8)}...
                  </Link>
                </TableCell>
                <TableCell className="font-mono text-sm">
                  {e.task_id.slice(0, 8)}...
                </TableCell>
                <TableCell>
                  <Badge className={statusColor[e.status] || ""}>
                    {e.status}
                  </Badge>
                </TableCell>
                <TableCell className="text-sm">
                  {e.started_at ? new Date(e.started_at).toLocaleString() : "-"}
                </TableCell>
                <TableCell className="text-sm">
                  {e.completed_at
                    ? new Date(e.completed_at).toLocaleString()
                    : "-"}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
