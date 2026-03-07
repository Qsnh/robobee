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
