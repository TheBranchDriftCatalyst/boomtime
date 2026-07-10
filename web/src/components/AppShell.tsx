import { useState } from "react";
import { NavLink, Outlet, useNavigate } from "react-router";
import {
  Award,
  BookOpen,
  Code2,
  Download,
  KeyRound,
  LayoutDashboard,
  ListTree,
  LogOut,
  Settings2,
  User,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { CreateTokenModal } from "@/modals/CreateTokenModal";
import { TokenListModal } from "@/modals/TokenListModal";
import { ThemeToggle } from "@/components/ThemeToggle";
import { RendererToggle } from "@/viz/RendererToggle";
import { useAuth } from "@/hooks/useAuth";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";

const NAV = [
  { name: "Overview", icon: LayoutDashboard, to: "/app", end: true },
  { name: "Projects", icon: BookOpen, to: "/app/projects", end: false },
  { name: "Leaderboards", icon: Award, to: "/app/leaderboards", end: false },
  { name: "Heartbeats", icon: ListTree, to: "/app/heartbeats", end: false },
  { name: "Import", icon: Download, to: "/app/import", end: false },
  { name: "Settings", icon: Settings2, to: "/app/settings", end: false },
];

export function AppShell() {
  const { username, logout } = useAuth();
  const navigate = useNavigate();
  const [newToken, setNewToken] = useState<string | null>(null);
  const [tokensOpen, setTokensOpen] = useState(false);

  async function createToken() {
    try {
      const res = await api.createApiToken();
      setNewToken(res.apiToken);
    } catch {
      toast.error("Failed to create API token");
    }
  }

  async function handleLogout() {
    await logout();
    navigate("/login");
  }

  return (
    <div className="flex h-full min-h-screen bg-background">
      {/* Sidebar */}
      <aside className="hidden w-60 shrink-0 flex-col border-r bg-sidebar text-sidebar-foreground md:flex">
        <div className="flex h-16 items-center gap-2 border-b px-6">
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
            <Code2 className="h-5 w-5" />
          </div>
          <span className="text-lg font-semibold">Gakatime</span>
        </div>
        <nav className="flex-1 space-y-1 p-3">
          {NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              className={({ isActive }) =>
                cn(
                  "flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors",
                  isActive
                    ? "bg-sidebar-primary text-sidebar-primary-foreground"
                    : "text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
                )
              }
            >
              <item.icon className="h-4 w-4" />
              {item.name}
            </NavLink>
          ))}
        </nav>
        <div className="border-t p-3">
          <button
            onClick={handleLogout}
            className="flex w-full items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
          >
            <LogOut className="h-4 w-4" />
            Logout
          </button>
        </div>
      </aside>

      {/* Main */}
      <div className="flex flex-1 flex-col overflow-hidden">
        <header className="flex h-16 items-center justify-end gap-3 border-b bg-card px-6">
          <RendererToggle />
          <ThemeToggle />
          <Button
            variant="outline"
            size="sm"
            onClick={createToken}
            title="Create a new API token"
          >
            <KeyRound className="h-4 w-4" />
            New API token
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button className="flex items-center gap-2">
                <span className="hidden text-sm text-muted-foreground sm:inline">
                  {username}
                </span>
                <div className="flex h-9 w-9 items-center justify-center rounded-full bg-secondary text-sm font-semibold uppercase">
                  {username ? username.charAt(0) : <User className="h-4 w-4" />}
                </div>
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-48">
              <DropdownMenuLabel>{username}</DropdownMenuLabel>
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={() => setTokensOpen(true)}>
                <KeyRound className="h-4 w-4" />
                Tokens
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => navigate("/app/import")}>
                <Download className="h-4 w-4" />
                Import data
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem onSelect={handleLogout}>
                <LogOut className="h-4 w-4" />
                Logout
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </header>

        <main className="flex-1 overflow-y-auto p-6">
          <Outlet />
        </main>
      </div>

      <CreateTokenModal token={newToken} onClose={() => setNewToken(null)} />
      <TokenListModal open={tokensOpen} onClose={() => setTokensOpen(false)} />
    </div>
  );
}
