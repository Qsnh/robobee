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

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">Executions</h1>
      {error && <p className="text-red-500 mb-4">{error.message}</p>}

      {executions.length === 0 && !error && (
        <p className="text-muted-foreground">No executions yet.</p>
      )}

      {executions.length > 0 && (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>Worker</TableHead>
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
                    to={`/executions/${e.id}`}
                    className="font-mono text-sm hover:underline"
                  >
                    {e.id.slice(0, 8)}...
                  </Link>
                </TableCell>
                <TableCell>
                  <Link
                    to={`/workers/${e.worker_id}`}
                    className="font-mono text-sm hover:underline"
                  >
                    {e.worker_id.slice(0, 8)}...
                  </Link>
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
