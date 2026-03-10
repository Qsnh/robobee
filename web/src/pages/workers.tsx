import { useState } from "react"
import { Link } from "react-router-dom"
import { useWorkers, useCreateWorker, useDeleteWorker } from "@/hooks/use-workers"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
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

const statusColor: Record<string, string> = {
  idle: "bg-green-100 text-green-800",
  working: "bg-blue-100 text-blue-800",
  error: "bg-red-100 text-red-800",
}

export function Workers() {
  const { data: workers = [], error: fetchError } = useWorkers()
  const createWorker = useCreateWorker()
  const deleteWorker = useDeleteWorker()
  const [open, setOpen] = useState(false)
  const [name, setName] = useState("")
  const [description, setDescription] = useState("")
  const [prompt, setPrompt] = useState("")
  const [scheduleEnabled, setScheduleEnabled] = useState(false)
  const [scheduleDescription, setScheduleDescription] = useState("")
  const [workDir, setWorkDir] = useState("")

  const error = fetchError?.message || createWorker.error?.message || deleteWorker.error?.message || ""

  const handleCreate = async () => {
    await createWorker.mutateAsync({
      name,
      description,
      prompt: prompt || undefined,
      schedule_enabled: scheduleEnabled || undefined,
      schedule_description: scheduleEnabled ? scheduleDescription : undefined,
      work_dir: workDir || undefined,
    })
    setOpen(false)
    setName("")
    setDescription("")
    setPrompt("")
    setScheduleEnabled(false)
    setScheduleDescription("")
    setWorkDir("")
  }

  const handleDelete = async (id: string) => {
    if (!confirm("Delete this worker?")) return
    await deleteWorker.mutateAsync(id)
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">Workers</h1>
        <Dialog open={open} onOpenChange={setOpen}>
          <DialogTrigger render={<Button />}>
            Create Worker
          </DialogTrigger>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Create Worker</DialogTitle>
            </DialogHeader>
            <div className="space-y-4 max-h-[70vh] overflow-y-auto">
              <div>
                <Label htmlFor="name">Name</Label>
                <Input
                  id="name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="e.g. report-bot"
                />
              </div>
              <div>
                <Label htmlFor="desc">Description</Label>
                <Textarea
                  id="desc"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder="What does this worker do?"
                />
              </div>
              <div>
                <Label htmlFor="workdir">Work Directory (optional)</Label>
                <Input
                  id="workdir"
                  value={workDir}
                  onChange={(e) => setWorkDir(e.target.value)}
                  placeholder="Leave blank to use default"
                />
              </div>
              <div className="flex items-center gap-2">
                <input
                  type="checkbox"
                  id="schedule"
                  checked={scheduleEnabled}
                  onChange={(e) => setScheduleEnabled(e.target.checked)}
                />
                <Label htmlFor="schedule">Enable Schedule</Label>
              </div>
              {scheduleEnabled && (
                <>
                  <div>
                    <Label htmlFor="schedule-desc">定时描述</Label>
                    <Input
                      id="schedule-desc"
                      value={scheduleDescription}
                      onChange={(e) => setScheduleDescription(e.target.value)}
                      placeholder="例如：每天凌晨3点执行"
                    />
                  </div>
                  <div>
                    <Label htmlFor="prompt">Prompt</Label>
                    <Textarea
                      id="prompt"
                      value={prompt}
                      onChange={(e) => setPrompt(e.target.value)}
                      placeholder="The instruction this worker will execute on schedule..."
                      rows={4}
                    />
                  </div>
                </>
              )}

              <Button onClick={handleCreate} className="w-full">
                Create
              </Button>
            </div>
          </DialogContent>
        </Dialog>
      </div>

      {error && <p className="text-red-500 mb-4">{error}</p>}

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        {workers.map((w) => (
          <Card key={w.id}>
            <CardHeader className="pb-2">
              <div className="flex items-center justify-between">
                <Link to={`/workers/${w.id}`}>
                  <CardTitle className="text-lg hover:underline">
                    {w.name}
                  </CardTitle>
                </Link>
                <Badge className={statusColor[w.status] || ""}>
                  {w.status}
                </Badge>
              </div>
            </CardHeader>
            <CardContent>
              <p className="text-sm text-muted-foreground mb-2">
                {w.description || "No description"}
              </p>
              <p className="text-xs text-muted-foreground">
                {w.schedule_enabled ? `Schedule: ${w.schedule_description || w.cron_expression}` : "On-demand"}
              </p>
              <div className="flex gap-2 mt-3">
                <Link to={`/workers/${w.id}`}>
                  <Button variant="outline" size="sm">
                    View
                  </Button>
                </Link>
                <Button
                  variant="destructive"
                  size="sm"
                  onClick={() => handleDelete(w.id)}
                >
                  Delete
                </Button>
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
    </div>
  )
}
