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
