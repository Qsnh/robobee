"use client"

import { useEffect, useState } from "react"
import { useParams } from "next/navigation"
import Link from "next/link"
import { api } from "@/lib/api"
import type { Worker, Task } from "@/lib/types"
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
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"

export default function WorkerDetailPage() {
  const { id } = useParams<{ id: string }>()
  const [worker, setWorker] = useState<Worker | null>(null)
  const [tasks, setTasks] = useState<Task[]>([])
  const [error, setError] = useState("")
  const [taskDialogOpen, setTaskDialogOpen] = useState(false)
  const [msgDialogOpen, setMsgDialogOpen] = useState(false)
  const [message, setMessage] = useState("")

  // New task form
  const [taskName, setTaskName] = useState("")
  const [taskPlan, setTaskPlan] = useState("")
  const [taskRecipients, setTaskRecipients] = useState("")
  const [taskTrigger, setTaskTrigger] = useState("manual")
  const [taskCron, setTaskCron] = useState("")
  const [taskApproval, setTaskApproval] = useState(false)

  const load = () => {
    api.workers.get(id).then(setWorker).catch((e) => setError(e.message))
    api.tasks.listByWorker(id).then(setTasks).catch(() => {})
  }

  useEffect(() => { load() }, [id])

  const handleCreateTask = async () => {
    try {
      const recipients = taskRecipients.split(",").map((r) => r.trim()).filter(Boolean)
      await api.tasks.create(id, {
        name: taskName,
        plan: taskPlan,
        trigger_type: taskTrigger as Task["trigger_type"],
        cron_expression: taskCron,
        recipients,
        requires_approval: taskApproval,
      })
      setTaskDialogOpen(false)
      setTaskName("")
      setTaskPlan("")
      setTaskRecipients("")
      load()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to create task")
    }
  }

  const handleExecute = async (taskId: string) => {
    try {
      const exec = await api.tasks.execute(taskId)
      window.location.href = `/executions/${exec.id}`
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to execute task")
    }
  }

  const handleSendMessage = async () => {
    try {
      const exec = await api.message.send(id, message)
      setMsgDialogOpen(false)
      setMessage("")
      window.location.href = `/executions/${exec.id}`
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
          <p className="text-muted-foreground">{worker.email}</p>
        </div>
        <div className="flex gap-2">
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

      {error && <p className="text-red-500 mb-4">{error}</p>}

      <Tabs defaultValue="tasks">
        <TabsList>
          <TabsTrigger value="tasks">Tasks</TabsTrigger>
          <TabsTrigger value="info">Info</TabsTrigger>
        </TabsList>

        <TabsContent value="tasks" className="mt-4">
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-lg font-semibold">Tasks</h2>
            <Dialog open={taskDialogOpen} onOpenChange={setTaskDialogOpen}>
              <DialogTrigger render={<Button size="sm" />}>
                Add Task
              </DialogTrigger>
              <DialogContent>
                <DialogHeader>
                  <DialogTitle>Create Task</DialogTitle>
                </DialogHeader>
                <div className="space-y-4">
                  <div>
                    <Label>Name</Label>
                    <Input value={taskName} onChange={(e) => setTaskName(e.target.value)} />
                  </div>
                  <div>
                    <Label>Plan</Label>
                    <Textarea value={taskPlan} onChange={(e) => setTaskPlan(e.target.value)} rows={4} />
                  </div>
                  <div>
                    <Label>Recipients (comma-separated emails)</Label>
                    <Input value={taskRecipients} onChange={(e) => setTaskRecipients(e.target.value)} />
                  </div>
                  <div>
                    <Label>Trigger Type</Label>
                    <select
                      value={taskTrigger}
                      onChange={(e) => setTaskTrigger(e.target.value)}
                      className="w-full rounded-md border px-3 py-2 text-sm"
                    >
                      <option value="manual">Manual</option>
                      <option value="cron">Cron</option>
                      <option value="email">Email</option>
                    </select>
                  </div>
                  {taskTrigger === "cron" && (
                    <div>
                      <Label>Cron Expression</Label>
                      <Input value={taskCron} onChange={(e) => setTaskCron(e.target.value)} placeholder="0 9 * * *" />
                    </div>
                  )}
                  <div className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      id="approval"
                      checked={taskApproval}
                      onChange={(e) => setTaskApproval(e.target.checked)}
                    />
                    <Label htmlFor="approval">Requires Approval</Label>
                  </div>
                  <Button onClick={handleCreateTask} className="w-full">Create</Button>
                </div>
              </DialogContent>
            </Dialog>
          </div>

          {tasks.length === 0 && <p className="text-muted-foreground">No tasks yet.</p>}

          <div className="space-y-3">
            {tasks.map((t) => (
              <Card key={t.id}>
                <CardContent className="flex items-center justify-between py-4">
                  <div>
                    <p className="font-medium">{t.name}</p>
                    <p className="text-sm text-muted-foreground">
                      {t.trigger_type} {t.cron_expression && `(${t.cron_expression})`}
                      {t.requires_approval && " | Approval required"}
                    </p>
                  </div>
                  <div className="flex gap-2">
                    <Button size="sm" onClick={() => handleExecute(t.id)}>
                      Execute
                    </Button>
                    <Button
                      size="sm"
                      variant="destructive"
                      onClick={async () => {
                        await api.tasks.delete(t.id)
                        load()
                      }}
                    >
                      Delete
                    </Button>
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        </TabsContent>

        <TabsContent value="info" className="mt-4">
          <Card>
            <CardHeader>
              <CardTitle>Worker Info</CardTitle>
            </CardHeader>
            <CardContent className="space-y-2">
              <p><strong>ID:</strong> {worker.id}</p>
              <p><strong>Runtime:</strong> {worker.runtime_type}</p>
              <p><strong>Work Dir:</strong> {worker.work_dir}</p>
              <p><strong>Status:</strong> <Badge>{worker.status}</Badge></p>
              <p><strong>Created:</strong> {new Date(worker.created_at).toLocaleString()}</p>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  )
}
