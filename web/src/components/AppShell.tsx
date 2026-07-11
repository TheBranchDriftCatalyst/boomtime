import { useCallback, useEffect, useState } from "react";
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
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  Settings2,
  User,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { CreateTokenModal } from "@/modals/CreateTokenModal";
import { TokenListModal } from "@/modals/TokenListModal";
import { ThemeToggle } from "@/components/ThemeToggle";
import { useAuth } from "@/hooks/useAuth";
import { useSpaces, useSpaceMutations } from "@/hooks/useSpaces";
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

const SIDEBAR_STORAGE_KEY = "boomtime-sidebar-collapsed";

function readStoredCollapsed(): boolean {
  if (typeof window === "undefined") return false;
  try {
    return window.localStorage.getItem(SIDEBAR_STORAGE_KEY) === "true";
  } catch {
    return false;
  }
}

export function AppShell() {
  const { username, logout } = useAuth();
  const navigate = useNavigate();
  const [newToken, setNewToken] = useState<string | null>(null);
  const [tokensOpen, setTokensOpen] = useState(false);
  const [collapsed, setCollapsed] = useState<boolean>(readStoredCollapsed);

  // Spaces sidebar group + create flow.
  const { data: spaces } = useSpaces();
  const { create: createSpace } = useSpaceMutations();
  const [createOpen, setCreateOpen] = useState(false);
  const [spaceName, setSpaceName] = useState("");

  function submitCreateSpace(e: React.FormEvent) {
    e.preventDefault();
    const name = spaceName.trim();
    if (!name) return;
    createSpace.mutate(name, {
      onSuccess: (space) => {
        setCreateOpen(false);
        setSpaceName("");
        navigate(`/app/space/${space.id}`);
      },
      onError: () => toast.error("Failed to create space"),
    });
  }

  // Persist the collapsed preference so it survives reloads.
  useEffect(() => {
    try {
      window.localStorage.setItem(SIDEBAR_STORAGE_KEY, String(collapsed));
    } catch {
      // ignore storage failures
    }
  }, [collapsed]);

  const toggleCollapsed = useCallback(() => setCollapsed((c) => !c), []);

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
      {/* Sidebar — collapsible to an icon-only rail. */}
      <aside
        className={cn(
          "hidden shrink-0 flex-col border-r bg-sidebar text-sidebar-foreground transition-[width] duration-200 ease-in-out md:flex",
          collapsed ? "w-16" : "w-60",
        )}
      >
        <div
          className={cn(
            "flex h-16 items-center border-b",
            collapsed ? "justify-center px-0" : "gap-2 px-6",
          )}
        >
          <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-primary text-primary-foreground">
            <Code2 className="h-5 w-5" />
          </div>
          {!collapsed && (
            <span className="text-lg font-semibold">Boomtime</span>
          )}
        </div>

        <nav className="flex-1 space-y-1 p-3">
          {NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              title={collapsed ? item.name : undefined}
              aria-label={item.name}
              className={({ isActive }) =>
                cn(
                  "flex items-center rounded-lg py-2 text-sm font-medium transition-colors",
                  collapsed ? "justify-center px-0" : "gap-3 px-3",
                  isActive
                    ? "bg-sidebar-primary text-sidebar-primary-foreground"
                    : "text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
                )
              }
            >
              <item.icon className="h-4 w-4 shrink-0" />
              {!collapsed && item.name}
            </NavLink>
          ))}

          {/* Spaces — dynamic, user-created scoped dashboards. */}
          <div className="pt-4">
            {!collapsed && (
              <div className="flex items-center justify-between px-3 pb-1">
                <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  Spaces
                </span>
              </div>
            )}
            {collapsed && (
              <div className="mx-3 mb-1 border-t border-sidebar-border" />
            )}

            {(spaces ?? []).map((space) => {
              const initial = space.name.trim().charAt(0).toUpperCase() || "S";
              return (
                <NavLink
                  key={space.id}
                  to={`/app/space/${space.id}`}
                  title={collapsed ? space.name : undefined}
                  aria-label={space.name}
                  className={({ isActive }) =>
                    cn(
                      "flex items-center rounded-lg py-2 text-sm font-medium transition-colors",
                      collapsed ? "justify-center px-0" : "gap-3 px-3",
                      isActive
                        ? "bg-sidebar-primary text-sidebar-primary-foreground"
                        : "text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
                    )
                  }
                >
                  <span className="flex h-4 w-4 shrink-0 items-center justify-center rounded-sm bg-secondary text-[10px] font-semibold text-secondary-foreground">
                    {initial}
                  </span>
                  {!collapsed && <span className="truncate">{space.name}</span>}
                </NavLink>
              );
            })}

            <button
              onClick={() => setCreateOpen(true)}
              title={collapsed ? "New space" : undefined}
              aria-label="New space"
              className={cn(
                "flex w-full items-center rounded-lg py-2 text-sm font-medium text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
                collapsed ? "justify-center px-0" : "gap-3 px-3",
              )}
            >
              <Plus className="h-4 w-4 shrink-0" />
              {!collapsed && "New space"}
            </button>
          </div>
        </nav>

        <div className="space-y-1 border-t p-3">
          <button
            onClick={handleLogout}
            title={collapsed ? "Logout" : undefined}
            aria-label="Logout"
            className={cn(
              "flex w-full items-center rounded-lg py-2 text-sm font-medium text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
              collapsed ? "justify-center px-0" : "gap-3 px-3",
            )}
          >
            <LogOut className="h-4 w-4 shrink-0" />
            {!collapsed && "Logout"}
          </button>

          <button
            onClick={toggleCollapsed}
            title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            aria-expanded={!collapsed}
            className={cn(
              "flex w-full items-center rounded-lg py-2 text-sm font-medium text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
              collapsed ? "justify-center px-0" : "gap-3 px-3",
            )}
          >
            {collapsed ? (
              <PanelLeftOpen className="h-4 w-4 shrink-0" />
            ) : (
              <PanelLeftClose className="h-4 w-4 shrink-0" />
            )}
            {!collapsed && "Collapse"}
          </button>
        </div>
      </aside>

      {/* Main */}
      <div className="flex flex-1 flex-col overflow-hidden">
        <header className="flex h-16 items-center justify-end gap-3 border-b bg-card px-6">
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

      <Dialog
        open={createOpen}
        onOpenChange={(o) => {
          setCreateOpen(o);
          if (!o) setSpaceName("");
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>New space</DialogTitle>
          </DialogHeader>
          <form onSubmit={submitCreateSpace} className="space-y-4">
            <div className="space-y-1">
              <Label htmlFor="space-name">Name</Label>
              <Input
                id="space-name"
                value={spaceName}
                onChange={(e) => setSpaceName(e.target.value)}
                placeholder="Work"
                autoFocus
              />
            </div>
            <DialogFooter>
              <Button
                type="button"
                variant="secondary"
                onClick={() => setCreateOpen(false)}
              >
                Cancel
              </Button>
              <Button
                type="submit"
                disabled={createSpace.isPending || spaceName.trim() === ""}
              >
                {createSpace.isPending ? "Creating..." : "Create"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  );
}
