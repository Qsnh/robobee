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
