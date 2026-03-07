# Next.js to React + React Router + shadcn/ui Migration Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the Next.js frontend (`/web`) with a Vite + React Router + TanStack Query SPA while preserving the existing base-nova shadcn/ui components.

**Architecture:** Vite-based React SPA with React Router v6 for client-side routing and TanStack Query for data fetching. All existing shadcn/ui components (base-nova style using @base-ui/react) are copied as-is. Pages are refactored to use TanStack Query hooks instead of raw useState/useEffect.

**Tech Stack:** Vite, React 19, React Router v6, TanStack Query v5, Tailwind CSS v4, shadcn/ui (base-nova), @base-ui/react, TypeScript

---

### Task 1: Scaffold Vite project in /web

**Files:**
- Create: `web/index.html`
- Create: `web/vite.config.ts`
- Create: `web/tsconfig.json` (replace existing)
- Create: `web/tsconfig.app.json`
- Create: `web/tsconfig.node.json`
- Create: `web/package.json` (replace existing)
- Create: `web/postcss.config.mjs` (replace existing)
- Delete: `web/next.config.ts`
- Delete: `web/next-env.d.ts`
- Delete: `web/.next/` (if exists)

**Step 1: Back up existing source and clean Next.js files**

```bash
cd /Users/tengteng/work/robobee/core/web
# Save source files we'll reuse
cp -r src/components src_components_backup
cp -r src/lib src_lib_backup
cp -r src/app/globals.css globals_css_backup
# Remove Next.js-specific files
rm -f next.config.ts next-env.d.ts
rm -rf .next
rm -rf src/app
```

**Step 2: Create package.json**

```json
{
  "name": "web",
  "private": true,
  "version": "0.1.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "preview": "vite preview",
    "lint": "eslint ."
  },
  "dependencies": {
    "@base-ui/react": "^1.2.0",
    "@tanstack/react-query": "^5.64.0",
    "class-variance-authority": "^0.7.1",
    "clsx": "^2.1.1",
    "lucide-react": "^0.577.0",
    "react": "^19.2.3",
    "react-dom": "^19.2.3",
    "react-router-dom": "^7.5.0",
    "tailwind-merge": "^3.5.0",
    "tw-animate-css": "^1.4.0"
  },
  "devDependencies": {
    "@tailwindcss/postcss": "^4",
    "@types/react": "^19",
    "@types/react-dom": "^19",
    "@vitejs/plugin-react": "^4.5.0",
    "eslint": "^9",
    "tailwindcss": "^4",
    "typescript": "^5",
    "vite": "^6.3.0"
  }
}
```

**Step 3: Create vite.config.ts**

```typescript
import { defineConfig } from "vite"
import react from "@vitejs/plugin-react"
import path from "path"

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
})
```

**Step 4: Create tsconfig.json**

```json
{
  "files": [],
  "references": [
    { "path": "./tsconfig.app.json" },
    { "path": "./tsconfig.node.json" }
  ]
}
```

**Step 5: Create tsconfig.app.json**

```json
{
  "compilerOptions": {
    "target": "ES2020",
    "useDefineForClassFields": true,
    "lib": ["ES2020", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "isolatedModules": true,
    "moduleDetection": "force",
    "noEmit": true,
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": false,
    "noUnusedParameters": false,
    "noFallthroughCasesInSwitch": true,
    "paths": {
      "@/*": ["./src/*"]
    }
  },
  "include": ["src"]
}
```

**Step 6: Create tsconfig.node.json**

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "lib": ["ES2023"],
    "module": "ESNext",
    "skipLibCheck": true,
    "moduleResolution": "bundler",
    "allowImportingTsExtensions": true,
    "isolatedModules": true,
    "moduleDetection": "force",
    "noEmit": true,
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noFallthroughCasesInSwitch": true
  },
  "include": ["vite.config.ts"]
}
```

**Step 7: Create postcss.config.mjs**

```javascript
/** @type {import('postcss-load-config').Config} */
const config = {
  plugins: {
    "@tailwindcss/postcss": {},
  },
}

export default config
```

**Step 8: Create index.html**

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>RoboBee - Digital Worker Dispatch</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

**Step 9: Update components.json**

Change `"rsc": true` to `"rsc": false` and update the CSS path:

```json
{
  "$schema": "https://ui.shadcn.com/schema.json",
  "style": "base-nova",
  "rsc": false,
  "tsx": true,
  "tailwind": {
    "config": "",
    "css": "src/globals.css",
    "baseColor": "neutral",
    "cssVariables": true,
    "prefix": ""
  },
  "iconLibrary": "lucide",
  "rtl": false,
  "aliases": {
    "components": "@/components",
    "utils": "@/lib/utils",
    "ui": "@/components/ui",
    "lib": "@/lib",
    "hooks": "@/hooks"
  },
  "menuColor": "default",
  "menuAccent": "subtle",
  "registries": {}
}
```

**Step 10: Install dependencies**

```bash
cd /Users/tengteng/work/robobee/core/web
npm install
```

**Step 11: Commit**

```bash
git add web/
git commit -m "chore: scaffold Vite project, remove Next.js config"
```

---

### Task 2: Set up source directory structure and copy unchanged files

**Files:**
- Create: `web/src/globals.css` (from backup)
- Create: `web/src/lib/utils.ts` (from backup)
- Create: `web/src/lib/types.ts` (from backup)
- Create: `web/src/components/ui/` (all 10 files from backup)

**Step 1: Create directory structure**

```bash
cd /Users/tengteng/work/robobee/core/web
mkdir -p src/lib src/hooks src/pages src/components/ui
```

**Step 2: Copy unchanged files**

```bash
# CSS - move from app/ location to src/ root
cp globals_css_backup src/globals.css

# Lib files - copy as-is
cp src_lib_backup/utils.ts src/lib/utils.ts
cp src_lib_backup/types.ts src/lib/types.ts

# UI components - copy all as-is
cp src_components_backup/ui/*.tsx src/components/ui/
```

**Step 3: Remove "use client" directives from UI components**

Remove the `"use client"` line from these files (not needed in Vite/SPA):
- `src/components/ui/button.tsx`
- `src/components/ui/dialog.tsx`
- `src/components/ui/label.tsx`
- `src/components/ui/select.tsx`
- `src/components/ui/table.tsx`
- `src/components/ui/tabs.tsx`

The following files don't have `"use client"` and need no changes:
- `src/components/ui/badge.tsx`
- `src/components/ui/card.tsx`
- `src/components/ui/input.tsx`
- `src/components/ui/textarea.tsx`

For each file with `"use client"`, remove just that first line. For example in `button.tsx`, the file should start with:

```typescript
import { Button as ButtonPrimitive } from "@base-ui/react/button"
```

**Step 4: Clean up backups**

```bash
cd /Users/tengteng/work/robobee/core/web
rm -rf src_components_backup src_lib_backup globals_css_backup
```

**Step 5: Commit**

```bash
git add web/src/
git commit -m "chore: copy unchanged files (CSS, types, utils, UI components)"
```

---

### Task 3: Create API client (Vite env vars)

**Files:**
- Create: `web/src/lib/api.ts`
- Create: `web/.env` (for local development)

**Step 1: Create .env file**

```
VITE_API_URL=http://localhost:8080/api
```

**Step 2: Create api.ts**

```typescript
import type { Worker, Task, TaskExecution, Email } from "./types"

const API_BASE = import.meta.env.VITE_API_URL || "http://localhost:8080/api"

async function fetchAPI<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    headers: { "Content-Type": "application/json" },
    ...options,
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  return res.json()
}

export const api = {
  workers: {
    list: () => fetchAPI<Worker[]>("/workers"),
    get: (id: string) => fetchAPI<Worker>(`/workers/${id}`),
    create: (data: { name: string; description: string; runtime_type: string }) =>
      fetchAPI<Worker>("/workers", { method: "POST", body: JSON.stringify(data) }),
    update: (id: string, data: Partial<Worker>) =>
      fetchAPI<Worker>(`/workers/${id}`, { method: "PUT", body: JSON.stringify(data) }),
    delete: (id: string) => fetchAPI(`/workers/${id}`, { method: "DELETE" }),
  },
  tasks: {
    listByWorker: (workerId: string) => fetchAPI<Task[]>(`/workers/${workerId}/tasks`),
    create: (workerId: string, data: Omit<Task, "id" | "worker_id" | "created_at" | "updated_at">) =>
      fetchAPI<Task>(`/workers/${workerId}/tasks`, { method: "POST", body: JSON.stringify(data) }),
    update: (id: string, data: Partial<Task>) =>
      fetchAPI<Task>(`/tasks/${id}`, { method: "PUT", body: JSON.stringify(data) }),
    delete: (id: string) => fetchAPI(`/tasks/${id}`, { method: "DELETE" }),
    execute: (id: string) =>
      fetchAPI<TaskExecution>(`/tasks/${id}/execute`, { method: "POST" }),
  },
  executions: {
    list: () => fetchAPI<TaskExecution[]>("/executions"),
    get: (id: string) => fetchAPI<TaskExecution>(`/executions/${id}`),
    approve: (id: string) => fetchAPI(`/executions/${id}/approve`, { method: "POST" }),
    reject: (id: string, feedback: string) =>
      fetchAPI(`/executions/${id}/reject`, { method: "POST", body: JSON.stringify({ feedback }) }),
    emails: (id: string) => fetchAPI<Email[]>(`/executions/${id}/emails`),
  },
  message: {
    send: (workerId: string, message: string, taskId?: string) =>
      fetchAPI<TaskExecution>(`/workers/${workerId}/message`, {
        method: "POST",
        body: JSON.stringify({ message, task_id: taskId }),
      }),
  },
}
```

**Step 3: Add Vite env type declaration**

Create `web/src/vite-env.d.ts`:

```typescript
/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_API_URL: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
```

**Step 4: Commit**

```bash
git add web/src/lib/api.ts web/src/vite-env.d.ts web/.env
git commit -m "feat: create API client with Vite env vars"
```

---

### Task 4: Create TanStack Query hooks

**Files:**
- Create: `web/src/hooks/use-workers.ts`
- Create: `web/src/hooks/use-tasks.ts`
- Create: `web/src/hooks/use-executions.ts`

**Step 1: Create use-workers.ts**

```typescript
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api"

export function useWorkers() {
  return useQuery({
    queryKey: ["workers"],
    queryFn: api.workers.list,
  })
}

export function useWorker(id: string) {
  return useQuery({
    queryKey: ["workers", id],
    queryFn: () => api.workers.get(id),
  })
}

export function useCreateWorker() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: { name: string; description: string; runtime_type: string }) =>
      api.workers.create(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["workers"] })
    },
  })
}

export function useDeleteWorker() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.workers.delete(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["workers"] })
    },
  })
}
```

**Step 2: Create use-tasks.ts**

```typescript
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api"
import type { Task } from "@/lib/types"

export function useTasks(workerId: string) {
  return useQuery({
    queryKey: ["tasks", workerId],
    queryFn: () => api.tasks.listByWorker(workerId),
  })
}

export function useCreateTask(workerId: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (data: Omit<Task, "id" | "worker_id" | "created_at" | "updated_at">) =>
      api.tasks.create(workerId, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["tasks", workerId] })
    },
  })
}

export function useDeleteTask(workerId: string) {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.tasks.delete(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["tasks", workerId] })
    },
  })
}

export function useExecuteTask() {
  return useMutation({
    mutationFn: (id: string) => api.tasks.execute(id),
  })
}
```

**Step 3: Create use-executions.ts**

```typescript
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query"
import { api } from "@/lib/api"

export function useExecutions() {
  return useQuery({
    queryKey: ["executions"],
    queryFn: api.executions.list,
  })
}

export function useExecution(id: string) {
  return useQuery({
    queryKey: ["executions", id],
    queryFn: () => api.executions.get(id),
  })
}

export function useExecutionEmails(id: string) {
  return useQuery({
    queryKey: ["executions", id, "emails"],
    queryFn: () => api.executions.emails(id),
  })
}

export function useApproveExecution() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.executions.approve(id),
    onSuccess: (_data, id) => {
      queryClient.invalidateQueries({ queryKey: ["executions", id] })
    },
  })
}

export function useRejectExecution() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ id, feedback }: { id: string; feedback: string }) =>
      api.executions.reject(id, feedback),
    onSuccess: (_data, { id }) => {
      queryClient.invalidateQueries({ queryKey: ["executions", id] })
    },
  })
}

export function useSendMessage() {
  return useMutation({
    mutationFn: ({ workerId, message, taskId }: { workerId: string; message: string; taskId?: string }) =>
      api.message.send(workerId, message, taskId),
  })
}
```

**Step 4: Commit**

```bash
git add web/src/hooks/
git commit -m "feat: add TanStack Query hooks for workers, tasks, executions"
```

---

### Task 5: Create layout, nav, and app entry point

**Files:**
- Create: `web/src/main.tsx`
- Create: `web/src/app.tsx`
- Create: `web/src/components/nav.tsx`
- Create: `web/src/components/layout.tsx`

**Step 1: Create nav.tsx**

Replace `next/link` with `react-router-dom` Link, and `usePathname` with `useLocation`:

```tsx
import { Link, useLocation } from "react-router-dom"
import { cn } from "@/lib/utils"

const links = [
  { href: "/", label: "Dashboard" },
  { href: "/workers", label: "Workers" },
  { href: "/executions", label: "Executions" },
]

export function Nav() {
  const { pathname } = useLocation()

  return (
    <nav className="border-b bg-background">
      <div className="container mx-auto flex h-14 items-center px-4">
        <Link to="/" className="mr-8 text-lg font-bold">
          RoboBee
        </Link>
        <div className="flex gap-4">
          {links.map((link) => (
            <Link
              key={link.href}
              to={link.href}
              className={cn(
                "text-sm font-medium transition-colors hover:text-primary",
                pathname === link.href
                  ? "text-foreground"
                  : "text-muted-foreground"
              )}
            >
              {link.label}
            </Link>
          ))}
        </div>
      </div>
    </nav>
  )
}
```

**Step 2: Create layout.tsx**

```tsx
import { Outlet } from "react-router-dom"
import { Nav } from "./nav"

export function Layout() {
  return (
    <div className="antialiased">
      <Nav />
      <main className="container mx-auto px-4 py-6">
        <Outlet />
      </main>
    </div>
  )
}
```

**Step 3: Create app.tsx**

```tsx
import { BrowserRouter, Routes, Route } from "react-router-dom"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { Layout } from "@/components/layout"
import { Dashboard } from "@/pages/dashboard"
import { Workers } from "@/pages/workers"
import { WorkerDetail } from "@/pages/worker-detail"
import { Executions } from "@/pages/executions"
import { ExecutionDetail } from "@/pages/execution-detail"

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
    },
  },
})

export function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Routes>
          <Route element={<Layout />}>
            <Route path="/" element={<Dashboard />} />
            <Route path="/workers" element={<Workers />} />
            <Route path="/workers/:id" element={<WorkerDetail />} />
            <Route path="/executions" element={<Executions />} />
            <Route path="/executions/:id" element={<ExecutionDetail />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  )
}
```

**Step 4: Create main.tsx**

```tsx
import { StrictMode } from "react"
import { createRoot } from "react-dom/client"
import { App } from "./app"
import "./globals.css"

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>
)
```

**Step 5: Commit**

```bash
git add web/src/main.tsx web/src/app.tsx web/src/components/nav.tsx web/src/components/layout.tsx
git commit -m "feat: add app entry point, routing, layout, and navigation"
```

---

### Task 6: Migrate Dashboard page

**Files:**
- Create: `web/src/pages/dashboard.tsx`

**Step 1: Create dashboard.tsx**

Convert from Next.js page to React component using TanStack Query. Replace `next/link` with `react-router-dom` Link:

```tsx
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
```

**Step 2: Commit**

```bash
git add web/src/pages/dashboard.tsx
git commit -m "feat: migrate Dashboard page to React Router + TanStack Query"
```

---

### Task 7: Migrate Workers page

**Files:**
- Create: `web/src/pages/workers.tsx`

**Step 1: Create workers.tsx**

Replace `useState/useEffect` data fetching with TanStack Query hooks. Replace `next/link` with `react-router-dom` Link:

```tsx
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
  const [runtimeType, setRuntimeType] = useState("claude_code")

  const error = fetchError?.message || createWorker.error?.message || deleteWorker.error?.message || ""

  const handleCreate = async () => {
    await createWorker.mutateAsync({ name, description, runtime_type: runtimeType })
    setOpen(false)
    setName("")
    setDescription("")
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
            <div className="space-y-4">
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
                <Label htmlFor="runtime">Runtime</Label>
                <select
                  id="runtime"
                  value={runtimeType}
                  onChange={(e) => setRuntimeType(e.target.value)}
                  className="w-full rounded-md border px-3 py-2 text-sm"
                >
                  <option value="claude_code">Claude Code</option>
                  <option value="codex">Codex</option>
                </select>
              </div>
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
              <p className="text-xs text-muted-foreground">{w.email}</p>
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
```

**Step 2: Commit**

```bash
git add web/src/pages/workers.tsx
git commit -m "feat: migrate Workers page to React Router + TanStack Query"
```

---

### Task 8: Migrate Worker Detail page

**Files:**
- Create: `web/src/pages/worker-detail.tsx`

**Step 1: Create worker-detail.tsx**

Replace `useParams` from `next/navigation` with `react-router-dom`. Replace `window.location.href` with `useNavigate()`. Use TanStack Query hooks:

```tsx
import { useState } from "react"
import { useParams, useNavigate } from "react-router-dom"
import { useWorker } from "@/hooks/use-workers"
import { useTasks, useCreateTask, useDeleteTask, useExecuteTask } from "@/hooks/use-tasks"
import { useSendMessage } from "@/hooks/use-executions"
import type { Task } from "@/lib/types"
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

export function WorkerDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { data: worker, error: workerError } = useWorker(id!)
  const { data: tasks = [] } = useTasks(id!)
  const createTask = useCreateTask(id!)
  const deleteTask = useDeleteTask(id!)
  const executeTask = useExecuteTask()
  const sendMessage = useSendMessage()

  const [taskDialogOpen, setTaskDialogOpen] = useState(false)
  const [msgDialogOpen, setMsgDialogOpen] = useState(false)
  const [message, setMessage] = useState("")
  const [error, setError] = useState("")

  // New task form
  const [taskName, setTaskName] = useState("")
  const [taskPlan, setTaskPlan] = useState("")
  const [taskRecipients, setTaskRecipients] = useState("")
  const [taskTrigger, setTaskTrigger] = useState("manual")
  const [taskCron, setTaskCron] = useState("")
  const [taskApproval, setTaskApproval] = useState(false)

  const handleCreateTask = async () => {
    try {
      const recipients = taskRecipients.split(",").map((r) => r.trim()).filter(Boolean)
      await createTask.mutateAsync({
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
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to create task")
    }
  }

  const handleExecute = async (taskId: string) => {
    try {
      const exec = await executeTask.mutateAsync(taskId)
      navigate(`/executions/${exec.id}`)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to execute task")
    }
  }

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

      {(error || workerError) && (
        <p className="text-red-500 mb-4">{error || workerError?.message}</p>
      )}

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
                        await deleteTask.mutateAsync(t.id)
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
```

**Step 2: Commit**

```bash
git add web/src/pages/worker-detail.tsx
git commit -m "feat: migrate Worker Detail page to React Router + TanStack Query"
```

---

### Task 9: Migrate Executions page

**Files:**
- Create: `web/src/pages/executions.tsx`

**Step 1: Create executions.tsx**

```tsx
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
  awaiting_approval: "bg-yellow-100 text-yellow-800",
  approved: "bg-green-100 text-green-800",
  rejected: "bg-red-100 text-red-800",
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
                    to={`/executions/${e.id}`}
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
```

**Step 2: Commit**

```bash
git add web/src/pages/executions.tsx
git commit -m "feat: migrate Executions page to React Router + TanStack Query"
```

---

### Task 10: Migrate Execution Detail page

**Files:**
- Create: `web/src/pages/execution-detail.tsx`

**Step 1: Create execution-detail.tsx**

The WebSocket logic stays the same. Replace `useParams` from `next/navigation` with `react-router-dom`. Use TanStack Query for data fetching:

```tsx
import { useEffect, useRef, useState } from "react"
import { useParams } from "react-router-dom"
import { useExecution, useExecutionEmails, useApproveExecution, useRejectExecution } from "@/hooks/use-executions"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Badge } from "@/components/ui/badge"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Textarea } from "@/components/ui/textarea"

const statusColor: Record<string, string> = {
  pending: "bg-gray-100 text-gray-800",
  running: "bg-blue-100 text-blue-800",
  awaiting_approval: "bg-yellow-100 text-yellow-800",
  approved: "bg-green-100 text-green-800",
  rejected: "bg-red-100 text-red-800",
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
  const { data: emails = [] } = useExecutionEmails(id!)
  const approveExecution = useApproveExecution()
  const rejectExecution = useRejectExecution()

  const [logs, setLogs] = useState<LogEntry[]>([])
  const [feedback, setFeedback] = useState("")
  const [error, setError] = useState("")
  const logsEndRef = useRef<HTMLDivElement>(null)

  // WebSocket for live logs
  useEffect(() => {
    const wsBase = import.meta.env.VITE_API_URL || "http://localhost:8080/api"
    const wsUrl = wsBase.replace(/^http/, "ws") + `/executions/${id}/logs`
    const ws = new WebSocket(wsUrl)

    ws.onmessage = (event) => {
      const data = JSON.parse(event.data)
      setLogs((prev) => [...prev, data])
    }

    ws.onerror = () => {
      // Connection might fail if execution is already done
    }

    return () => ws.close()
  }, [id])

  useEffect(() => {
    logsEndRef.current?.scrollIntoView({ behavior: "smooth" })
  }, [logs])

  const handleApprove = async () => {
    try {
      await approveExecution.mutateAsync(id!)
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to approve")
    }
  }

  const handleReject = async () => {
    try {
      await rejectExecution.mutateAsync({ id: id!, feedback })
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Failed to reject")
    }
  }

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

      {(error || fetchError) && (
        <p className="text-red-500 mb-4">{error || fetchError?.message}</p>
      )}

      {execution.status === "awaiting_approval" && (
        <Card className="mb-6 border-yellow-300">
          <CardHeader>
            <CardTitle>Approval Required</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <Textarea
              placeholder="Feedback (optional for rejection)"
              value={feedback}
              onChange={(e) => setFeedback(e.target.value)}
            />
            <div className="flex gap-2">
              <Button onClick={handleApprove}>Approve</Button>
              <Button variant="destructive" onClick={handleReject}>
                Reject
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      <Tabs defaultValue="logs">
        <TabsList>
          <TabsTrigger value="logs">Logs</TabsTrigger>
          <TabsTrigger value="result">Result</TabsTrigger>
          <TabsTrigger value="emails">Emails</TabsTrigger>
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

        <TabsContent value="emails" className="mt-4">
          {emails.length === 0 && (
            <p className="text-muted-foreground">No emails for this execution.</p>
          )}
          <div className="space-y-3">
            {emails.map((e) => (
              <Card key={e.id}>
                <CardHeader className="pb-2">
                  <div className="flex items-center justify-between">
                    <CardTitle className="text-sm">{e.subject}</CardTitle>
                    <Badge variant="outline">{e.direction}</Badge>
                  </div>
                </CardHeader>
                <CardContent>
                  <p className="text-xs text-muted-foreground mb-2">
                    From: {e.from_addr} | To: {e.to_addr}
                    {e.cc_addr && ` | CC: ${e.cc_addr}`}
                  </p>
                  <pre className="whitespace-pre-wrap text-sm">{e.body}</pre>
                </CardContent>
              </Card>
            ))}
          </div>
        </TabsContent>

        <TabsContent value="info" className="mt-4">
          <Card>
            <CardContent className="pt-6 space-y-2">
              <p><strong>Task ID:</strong> <span className="font-mono text-sm">{execution.task_id}</span></p>
              <p><strong>Session ID:</strong> <span className="font-mono text-sm">{execution.session_id}</span></p>
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
```

**Step 2: Commit**

```bash
git add web/src/pages/execution-detail.tsx
git commit -m "feat: migrate Execution Detail page to React Router + TanStack Query"
```

---

### Task 11: Verify build and run dev server

**Step 1: Run TypeScript type check**

```bash
cd /Users/tengteng/work/robobee/core/web
npx tsc -b --noEmit
```

Expected: No type errors.

**Step 2: Run Vite build**

```bash
cd /Users/tengteng/work/robobee/core/web
npm run build
```

Expected: Build succeeds, output in `dist/`.

**Step 3: Run dev server and verify**

```bash
cd /Users/tengteng/work/robobee/core/web
npm run dev
```

Expected: Dev server starts on `http://localhost:5173`, all routes load without errors.

**Step 4: Add .gitignore entries**

Ensure `web/.gitignore` has:
```
node_modules
dist
.env.local
```

**Step 5: Final commit**

```bash
git add web/
git commit -m "chore: verify build, clean up migration"
```

---

## Summary of Changes

| From (Next.js) | To (Vite + React Router) |
|---|---|
| `next dev` / `next build` | `vite` / `vite build` |
| `next/link` → `Link` | `react-router-dom` → `Link` (with `to` instead of `href`) |
| `next/navigation` → `useParams`, `usePathname`, `useRouter` | `react-router-dom` → `useParams`, `useLocation`, `useNavigate` |
| `"use client"` directive | Not needed (all client-side) |
| `process.env.NEXT_PUBLIC_*` | `import.meta.env.VITE_*` |
| `useState` + `useEffect` + `fetch` | TanStack Query (`useQuery`, `useMutation`) |
| `window.location.href = ...` | `navigate(...)` |
| App Router `layout.tsx` + `page.tsx` | `<BrowserRouter>` + `<Routes>` + `<Route>` |
| `next/font/google` (Geist) | System fonts (remove custom font loading) |
