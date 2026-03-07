import { Link } from "react-router-dom"
import { useWorkers } from "@/hooks/use-workers"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"

const statusColor: Record<string, string> = {
  idle: "bg-green-100 text-green-800",
  working: "bg-blue-100 text-blue-800",
  error: "bg-red-100 text-red-800",
}

export function Dashboard() {
  const { data: workers = [], error } = useWorkers()

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">Dashboard</h1>
      {error && <p className="text-red-500 mb-4">{error.message}</p>}
      {workers.length === 0 && !error && (
        <p className="text-muted-foreground">
          No workers yet.{" "}
          <Link to="/workers" className="underline">
            Create one
          </Link>
        </p>
      )}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {workers.map((w) => (
          <Link key={w.id} to={`/workers/${w.id}`}>
            <Card className="hover:shadow-md transition-shadow cursor-pointer">
              <CardHeader className="pb-2">
                <div className="flex items-center justify-between">
                  <CardTitle className="text-lg">{w.name}</CardTitle>
                  <Badge className={statusColor[w.status] || ""}>
                    {w.status}
                  </Badge>
                </div>
              </CardHeader>
              <CardContent>
                <p className="text-sm text-muted-foreground mb-1">
                  {w.description || "No description"}
                </p>
                <p className="text-xs text-muted-foreground">{w.email}</p>
                <p className="text-xs text-muted-foreground mt-1">
                  Runtime: {w.runtime_type} | {w.trigger_type === "cron" ? `Cron: ${w.cron_expression}` : "Message"}
                </p>
              </CardContent>
            </Card>
          </Link>
        ))}
      </div>
    </div>
  )
}
