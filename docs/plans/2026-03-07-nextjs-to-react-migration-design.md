# Next.js to React + React Router + shadcn/ui Migration Design

## Context

The RoboBee Core frontend is a Next.js App Router application under `/web`. All pages use `"use client"` with no SSR/SSG, no Next.js API routes, and the backend is a separate Go server. This makes it an ideal candidate for migration to a plain React SPA.

## Decision Summary

| Decision | Choice |
|----------|--------|
| Build tool | Vite |
| Routing | React Router v6 (SPA mode) |
| UI components | shadcn/ui base-nova style (@base-ui/react) |
| Data fetching | TanStack Query |
| Styling | Tailwind CSS v4 (unchanged) |
| Directory | Replace existing `/web` |

## Architecture

### Project Structure

```
web/
├── index.html
├── vite.config.ts
├── tsconfig.json
├── postcss.config.mjs
├── components.json
├── package.json
└── src/
    ├── main.tsx              # Entry: React + QueryClient + Router
    ├── app.tsx               # Route definitions
    ├── globals.css           # CSS variables + Tailwind (migrated as-is)
    ├── lib/
    │   ├── utils.ts          # cn() utility (unchanged)
    │   ├── api.ts            # API client (VITE_ env vars)
    │   └── types.ts          # Type definitions (unchanged)
    ├── hooks/
    │   ├── use-workers.ts    # TanStack Query hooks
    │   ├── use-executions.ts
    │   └── use-tasks.ts
    ├── components/
    │   ├── ui/               # shadcn/ui components (migrated as-is)
    │   ├── nav.tsx           # Navigation (react-router Link)
    │   └── layout.tsx        # Layout wrapper
    └── pages/
        ├── dashboard.tsx
        ├── workers.tsx
        ├── worker-detail.tsx
        ├── executions.tsx
        └── execution-detail.tsx
```

### Routing

```tsx
<BrowserRouter>
  <Layout>
    <Routes>
      <Route path="/" element={<Dashboard />} />
      <Route path="/workers" element={<Workers />} />
      <Route path="/workers/:id" element={<WorkerDetail />} />
      <Route path="/executions" element={<Executions />} />
      <Route path="/executions/:id" element={<ExecutionDetail />} />
    </Routes>
  </Layout>
</BrowserRouter>
```

### Data Layer

Replace `useState + useEffect + fetch` with TanStack Query:

- `useQuery` for data fetching with automatic caching and refetching
- `useMutation` with `invalidateQueries` for write operations
- WebSocket log streaming remains unchanged (not suitable for Query)

### Migration Scope

| Category | Action |
|----------|--------|
| UI components (`components/ui/*`) | Copy as-is |
| CSS (`globals.css`) | Copy as-is |
| Types (`types.ts`) | Copy as-is |
| Utils (`utils.ts`) | Copy as-is |
| API client (`api.ts`) | `NEXT_PUBLIC_` → `VITE_` |
| Page components | Remove `"use client"`, `useRouter()` → `useNavigate()`, `useParams` from react-router |
| Navigation | `next/link` → `react-router-dom` Link |
| Layout | App Router layout → React component |

### Dependencies

**Remove:** `next`, `eslint-config-next`
**Add:** `react-router-dom`, `@tanstack/react-query`, `vite`, `@vitejs/plugin-react`
**Keep:** `react`, `react-dom`, `@base-ui/react`, `tailwindcss`, `lucide-react`, `clsx`, `tailwind-merge`, `class-variance-authority`, `tw-animate-css`
