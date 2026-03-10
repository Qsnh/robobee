import { useState, useMemo } from "react"
import { useParams, useNavigate, Link } from "react-router-dom"
import { useWorker, useWorkerExecutions, useUpdateWorker } from "@/hooks/use-workers"
import { useSendMessage } from "@/hooks/use-executions"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { Textarea } from "@/components/ui/textarea"
import { Pencil } from "lucide-react"

const statusColor: Record<string, string> = {
  idle: "bg-green-100 text-green-800",
  working: "bg-blue-100 text-blue-800",
  error: "bg-red-100 text-red-800",
}

const execStatusColor: Record<string, string> = {
  pending: "bg-gray-100 text-gray-800",
  running: "bg-blue-100 text-blue-800",
  completed: "bg-green-100 text-green-800",
  failed: "bg-red-100 text-red-800",
}

export function WorkerDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: worker, error: workerError } = useWorker(id!)
  const { data: executions = [] } = useWorkerExecutions(id!)
  const sendMessage = useSendMessage()

  const sessionGroups = useMemo(() => {
    const map = new Map<string, typeof executions>()
    for (const e of executions) {
      const group = map.get(e.session_id) ?? []
      group.push(e)
      map.set(e.session_id, group)
    }
    return Array.from(map.values()).sort((a, b) => {
      const aTime = a[0].started_at ?? ""
      const bTime = b[0].started_at ?? ""
      return bTime.localeCompare(aTime)
    })
  }, [executions])

  const [isEditingDesc, setIsEditingDesc] = useState(false)
  const [editDesc, setEditDesc] = useState("")
  const updateWorker = useUpdateWorker()

  const [msgDialogOpen, setMsgDialogOpen] = useState(false)
  const [message, setMessage] = useState("")
  const [error, setError] = useState("")

  const handleSendMessage = async () => {
    try {
      const exec = await sendMessage.mutateAsync({ workerId: id!, message })
      setMsgDialogOpen(false)
      setMessage("")
      navigate(`/executions/${exec.id}`)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to send message")
    }
  }

  if (!worker) return <p>Loading...</p>

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <div>
          <h1 className="text-2xl font-bold">{worker.name}</h1>
          {isEditingDesc ? (
            <div className="mt-1 space-y-2">
              <Textarea
                value={editDesc}
                onChange={(e) => setEditDesc(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Escape") { setIsEditingDesc(false); setEditDesc(worker.description) }
                }}
                autoFocus
                rows={2}
                className="text-sm"
              />
              <div className="flex gap-2">
                <Button size="sm" onClick={async () => {
                  await updateWorker.mutateAsync({ id: id!, data: { description: editDesc } })
                  setIsEditingDesc(false)
                }}>Save</Button>
                <Button size="sm" variant="outline" onClick={() => {
                  setIsEditingDesc(false)
                  setEditDesc(worker.description)
                }}>Cancel</Button>
              </div>
            </div>
          ) : (
            <div className="group flex items-center gap-1 mt-1">
              <p className="text-muted-foreground text-sm">
                {worker.description || "No description"}
              </p>
              <button
                className="opacity-0 group-hover:opacity-100 transition-opacity text-muted-foreground hover:text-foreground"
                onClick={() => { setEditDesc(worker.description); setIsEditingDesc(true) }}
              >
                <Pencil className="h-3 w-3" />
              </button>
            </div>
          )}
        </div>
        <div className="flex gap-2 items-center">
          <Badge className={statusColor[worker.status] || ""}>{worker.status}</Badge>
          <Dialog open={msgDialogOpen} onOpenChange={setMsgDialogOpen}>
            <DialogTrigger render={<Button />}>
              Send Message
            </DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Send Message to {worker.name}</DialogTitle>
              </DialogHeader>
              <div className="space-y-4">
                <Textarea
                  value={message}
                  onChange={(e) => setMessage(e.target.value)}
                  placeholder="Enter your message..."
                  rows={4}
                />
                <Button onClick={handleSendMessage} className="w-full">
                  Send
                </Button>
              </div>
            </DialogContent>
          </Dialog>
        </div>
      </div>

      {(error || workerError) && (
        <p className="text-red-500 mb-4">{error || workerError?.message}</p>
      )}

      <Tabs defaultValue="executions">
        <TabsList>
          <TabsTrigger value="executions">Executions</TabsTrigger>
          <TabsTrigger value="info">Info</TabsTrigger>
        </TabsList>

        <TabsContent value="executions" className="mt-4">
          {sessionGroups.length === 0 && (
            <p className="text-muted-foreground">No executions yet.</p>
          )}
          <div className="space-y-3">
            {sessionGroups.map((group) => {
              const latest = group[0]
              const oldest = group[group.length - 1]
              return (
                <Card key={latest.session_id}>
                  <CardContent className="flex items-center justify-between py-4">
                    <div>
                      <Link
                        to={`/sessions/${latest.session_id}`}
                        className="font-mono text-sm hover:underline"
                      >
                        {latest.session_id.slice(0, 8)}...
                      </Link>
                      <p className="text-xs text-muted-foreground mt-1">
                        {oldest.started_at ? new Date(oldest.started_at).toLocaleString() : "-"}
                        {oldest.trigger_input && ` | ${oldest.trigger_input.slice(0, 50)}${oldest.trigger_input.length > 50 ? "..." : ""}`}
                      </p>
                      <p className="text-xs text-muted-foreground">
                        {group.length} turn{group.length !== 1 ? "s" : ""}
                      </p>
                    </div>
                    <Badge className={execStatusColor[latest.status] || ""}>
                      {latest.status}
                    </Badge>
                  </CardContent>
                </Card>
              )
            })}
          </div>
        </TabsContent>

        <TabsContent value="info" className="mt-4">
          <Card>
            <CardHeader>
              <CardTitle>Worker Info</CardTitle>
            </CardHeader>
            <CardContent className="space-y-2">
              <p><strong>ID:</strong> <span className="font-mono text-sm">{worker.id}</span></p>
              <p><strong>Schedule:</strong> {worker.schedule_enabled ? `Enabled (${worker.cron_expression})` : "Disabled"}</p>
              <p><strong>Work Dir:</strong> {worker.work_dir}</p>
              <p><strong>Created:</strong> {new Date(worker.created_at).toLocaleString()}</p>
              {worker.prompt && (
                <div>
                  <strong>Prompt:</strong>
                  <pre className="mt-1 whitespace-pre-wrap text-sm bg-muted p-3 rounded-md">
                    {worker.prompt}
                  </pre>
                </div>
              )}
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  )
}
