import { useMemo } from "react"
import { Link } from "react-router-dom"
import { useExecutions } from "@/hooks/use-executions"
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
  completed: "bg-green-100 text-green-800",
  failed: "bg-red-100 text-red-800",
}

export function Executions() {
  const { data: executions = [], error } = useExecutions()

  const sessionGroups = useMemo(() => {
    const map = new Map<string, typeof executions>()
    for (const e of executions) {
      const group = map.get(e.session_id) ?? []
      group.push(e)
      map.set(e.session_id, group)
    }
    // Each group: executions already ordered DESC from API, so first = latest
    return Array.from(map.values()).sort((a, b) => {
      const aTime = a[0].started_at ?? ""
      const bTime = b[0].started_at ?? ""
      return bTime.localeCompare(aTime)
    })
  }, [executions])

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">Executions</h1>
      {error && <p className="text-red-500 mb-4">{error.message}</p>}

      {sessionGroups.length === 0 && !error && (
        <p className="text-muted-foreground">No executions yet.</p>
      )}

      {sessionGroups.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Session</TableHead>
              <TableHead>Worker</TableHead>
              <TableHead>Turns</TableHead>
              <TableHead>Latest Status</TableHead>
              <TableHead>Started</TableHead>
              <TableHead>Last Completed</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {sessionGroups.map((group) => {
              const latest = group[0]
              const oldest = group[group.length - 1]
              const lastCompleted = group.find((e) => e.completed_at)
              return (
                <TableRow key={latest.session_id}>
                  <TableCell>
                    <Link
                      to={`/sessions/${latest.session_id}`}
                      className="font-mono text-sm hover:underline"
                    >
                      {latest.session_id.slice(0, 8)}...
                    </Link>
                  </TableCell>
                  <TableCell>
                    <Link
                      to={`/workers/${latest.worker_id}`}
                      className="font-mono text-sm hover:underline"
                    >
                      {latest.worker_id.slice(0, 8)}...
                    </Link>
                  </TableCell>
                  <TableCell className="text-sm">{group.length} turn{group.length !== 1 ? "s" : ""}</TableCell>
                  <TableCell>
                    <Badge className={statusColor[latest.status] || ""}>{latest.status}</Badge>
                  </TableCell>
                  <TableCell className="text-sm">
                    {oldest.started_at ? new Date(oldest.started_at).toLocaleString() : "-"}
                  </TableCell>
                  <TableCell className="text-sm">
                    {lastCompleted?.completed_at
                      ? new Date(lastCompleted.completed_at).toLocaleString()
                      : "-"}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
