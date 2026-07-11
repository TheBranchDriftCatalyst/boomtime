import { NavLink } from "react-router";
import { useQuery } from "@tanstack/react-query";
import {
  Award,
  BookOpen,
  Code2,
  Download,
  History,
  LayoutDashboard,
  ListTree,
  LogOut,
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  ScrollText,
  Settings2,
} from "lucide-react";
import { useSpaces } from "@/features/spaces/useSpaces";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { cn } from "@/lib/utils";

const NAV = [
  { name: "Overview", icon: LayoutDashboard, to: "/app", end: true },
  { name: "Projects", icon: BookOpen, to: "/app/projects", end: false },
  { name: "Leaderboards", icon: Award, to: "/app/leaderboards", end: false },
  { name: "Heartbeats", icon: ListTree, to: "/app/heartbeats", end: false },
  { name: "Import", icon: Download, to: "/app/import", end: false },
  { name: "Logs", icon: ScrollText, to: "/app/logs", end: false },
  { name: "Changelog", icon: History, to: "/app/changelog", end: false },
  { name: "Settings", icon: Settings2, to: "/app/settings", end: false },
];

// Single source for the sidebar item styling (nav links, space links, and the
// action buttons all share it; buttons pass isActive=false and add w-full).
function sidebarItemClass(collapsed: boolean, isActive: boolean): string {
  return cn(
    "flex items-center rounded-lg py-2 text-sm font-medium transition-colors",
    collapsed ? "justify-center px-0" : "gap-3 px-3",
    isActive
      ? "bg-sidebar-primary text-sidebar-primary-foreground"
      : "text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
  );
}

interface SidebarProps {
  collapsed: boolean;
  onToggleCollapsed: () => void;
  onLogout: () => void;
  onCreateSpace: () => void;
}

/** Spaces — dynamic, user-created scoped dashboards. */
function SpacesNavGroup({
  collapsed,
  onCreateSpace,
}: {
  collapsed: boolean;
  onCreateSpace: () => void;
}) {
  const { data: spaces } = useSpaces();

  return (
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
            className={({ isActive }) => sidebarItemClass(collapsed, isActive)}
          >
            <span className="flex h-4 w-4 shrink-0 items-center justify-center rounded-sm bg-secondary text-[10px] font-semibold text-secondary-foreground">
              {initial}
            </span>
            {!collapsed && <span className="truncate">{space.name}</span>}
          </NavLink>
        );
      })}

      <button
        onClick={onCreateSpace}
        title={collapsed ? "New space" : undefined}
        aria-label="New space"
        className={cn("w-full", sidebarItemClass(collapsed, false))}
      >
        <Plus className="h-4 w-4 shrink-0" />
        {!collapsed && "New space"}
      </button>
    </div>
  );
}

/** App sidebar: brand, nav items, the Spaces group, and the footer actions. */
export function Sidebar({
  collapsed,
  onToggleCollapsed,
  onLogout,
  onCreateSpace,
}: SidebarProps) {
  return (
    /* Sidebar — collapsible to an icon-only rail. */
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
            className={({ isActive }) => sidebarItemClass(collapsed, isActive)}
          >
            <item.icon className="h-4 w-4 shrink-0" />
            {!collapsed && item.name}
          </NavLink>
        ))}

        <SpacesNavGroup collapsed={collapsed} onCreateSpace={onCreateSpace} />
      </nav>

      <div className="space-y-1 border-t p-3">
        <button
          onClick={onLogout}
          title={collapsed ? "Logout" : undefined}
          aria-label="Logout"
          className={cn("w-full", sidebarItemClass(collapsed, false))}
        >
          <LogOut className="h-4 w-4 shrink-0" />
          {!collapsed && "Logout"}
        </button>

        <button
          onClick={onToggleCollapsed}
          title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          aria-expanded={!collapsed}
          className={cn("w-full", sidebarItemClass(collapsed, false))}
        >
          {collapsed ? (
            <PanelLeftOpen className="h-4 w-4 shrink-0" />
          ) : (
            <PanelLeftClose className="h-4 w-4 shrink-0" />
          )}
          {!collapsed && "Collapse"}
        </button>

        {!collapsed && <SidebarVersion />}
      </div>
    </aside>
  );
}

/** Small running-version chip at the sidebar footer. Fails silently if the
 * endpoint is unreachable (never blocks the layout). */
function SidebarVersion() {
  const { data } = useQuery({
    queryKey: qk.version(),
    queryFn: () => api.getVersion(),
    staleTime: Infinity,
    retry: false,
  });
  if (!data?.version) return null;
  return (
    <NavLink
      to="/app/changelog"
      className="mt-1 block text-center font-mono text-[10px] text-muted-foreground hover:text-foreground"
      title="View changelog"
    >
      {data.version}
    </NavLink>
  );
}
