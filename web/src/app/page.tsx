"use client"

import { useEffect, useState } from "react"
import Link from "next/link"
import { api } from "@/lib/api"
import type { Worker } from "@/lib/types"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"

const statusColor: Record<string, string> = {
  idle: "bg-green-100 text-green-800",
  working: "bg-blue-100 text-blue-800",
  error: "bg-red-100 text-red-800",
}

export default function Dashboard() {
  const [workers, setWorkers] = useState<Worker[]>([])
  const [error, setError] = useState("")

  useEffect(() => {
    api.workers.list().then(setWorkers).catch((e) => setError(e.message))
  }, [])

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">Dashboard</h1>
      {error && <p className="text-red-500 mb-4">{error}</p>}
      {workers.length === 0 && !error && (
        <p className="text-muted-foreground">
          No workers yet.{" "}
          <Link href="/workers" className="underline">
            Create one
          </Link>
        </p>
      )}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {workers.map((w) => (
          <Link key={w.id} href={`/workers/${w.id}`}>
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
                  Runtime: {w.runtime_type}
                </p>
              </CardContent>
            </Card>
          </Link>
        ))}
      </div>
    </div>
  )
}
