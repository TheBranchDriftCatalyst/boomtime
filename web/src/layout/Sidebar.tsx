import type { ReactNode } from "react";
import { NavLink } from "react-router";
import { useQuery } from "@tanstack/react-query";
import {
  Award,
  BookOpen,
  Download,
  HeartPulse,
  LayoutDashboard,
  ListTree,
  LogOut,
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  Settings2,
  Shapes,
  ShieldCheck,
  Target,
  UserCircle,
} from "lucide-react";
import { useSpaces } from "@/features/spaces/useSpaces";
import { useIsAdmin } from "@/features/auth/useIsAdmin";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { cn } from "@/lib/utils";

const NAV = [
  { name: "Overview", icon: LayoutDashboard, to: "/app", end: true },
  { name: "Projects", icon: BookOpen, to: "/app/projects", end: false },
  { name: "Leaderboards", icon: Award, to: "/app/leaderboards", end: false },
  // gaka-gud: Goals promoted from a Settings sub-tab to a top-level page.
  { name: "Goals", icon: Target, to: "/app/goals", end: false },
  { name: "Heartbeats", icon: ListTree, to: "/app/heartbeats", end: false },
  { name: "Wellness", icon: HeartPulse, to: "/app/wellness", end: false },
  { name: "Catalog", icon: Shapes, to: "/app/catalog", end: false },
  { name: "Import", icon: Download, to: "/app/import", end: false },
  // Logs + Changelog live inside Settings tabs now.
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

/** Spaces — dynamic, user-created scoped dashboards. Also hosts the
 * public-profile link (it's semantically a scoped, publishable view of
 * the caller's data — a "space" by any reasonable stretch). The
 * publicProfileSlot is rendered ABOVE the user-created spaces list so
 * operators see their public link first when scanning the group. The
 * inner component decides whether to render (only if the caller has
 * their public profile enabled) so passing null here means no slot. */
function SpacesNavGroup({
  collapsed,
  onCreateSpace,
  publicProfileSlot,
}: {
  collapsed: boolean;
  onCreateSpace: () => void;
  publicProfileSlot?: ReactNode;
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

      {publicProfileSlot}

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

/** ProfileNavLink — the single Profile entry (gaka-4ng), living in the Spaces
 * group because a profile is semantically a "space" too: a scoped, publishable
 * view of your data. Points at the IN-APP owner view (/app/profile), which
 * hosts both the dossier preview and the editor; the shareable public /p/:slug
 * URL is reachable from within that page. Always shown for the logged-in owner
 * (unlike the old external link, which hid until a public profile was enabled)
 * so they can always reach — and set up — their profile. */
function ProfileNavLink({ collapsed }: { collapsed: boolean }) {
  return (
    <NavLink
      to="/app/profile"
      title={collapsed ? "Profile" : undefined}
      aria-label="Profile"
      className={({ isActive }) => sidebarItemClass(collapsed, isActive)}
      data-testid="sidebar-public-profile"
    >
      <UserCircle className="h-4 w-4 shrink-0" />
      {!collapsed && "Profile"}
    </NavLink>
  );
}

/** AdminNavLink — top-level Admin section entry (gaka-ebq). Rendered only
 * when the current user is on BOOM_ADMIN_USERS. Matches active on any
 * /app/admin descendant so a sub-tab (labels/backfill/logs) still lights
 * up the parent link. Hidden entirely (not disabled, not "unauthorized")
 * for non-admins — same visual model as PublicProfileNavLink: if it isn't
 * yours, it isn't in the sidebar. */
function AdminNavLink({ collapsed }: { collapsed: boolean }) {
  const { isAdmin, isLoading } = useIsAdmin();
  // Render nothing during the first-paint auth check so we don't flash a
  // link in for admins-of-record who reload. The Overview page is happy
  // to render behind us regardless.
  if (isLoading || !isAdmin) return null;

  return (
    <NavLink
      to="/app/admin"
      title={collapsed ? "Admin" : undefined}
      aria-label="Admin"
      className={({ isActive }) => sidebarItemClass(collapsed, isActive)}
      data-testid="sidebar-admin"
    >
      <ShieldCheck className="h-4 w-4 shrink-0" />
      {!collapsed && "Admin"}
    </NavLink>
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
        <img
          src="/boomtime.svg"
          alt=""
          aria-hidden="true"
          className="h-8 w-8 shrink-0 rounded-lg"
        />
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

        <AdminNavLink collapsed={collapsed} />

        {/* Profile lives in the Spaces group — it's semantically a "space" too
            (a scoped, publishable view of your data). Order: Profile first,
            then user-created Spaces, then New space. */}
        <SpacesNavGroup
          collapsed={collapsed}
          onCreateSpace={onCreateSpace}
          publicProfileSlot={<ProfileNavLink collapsed={collapsed} />}
        />
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

        {!collapsed && (
          <>
            <SidebarVersion />
            <SidebarAttribution />
          </>
        )}
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

// Small OSS-style attribution under the version chip. Low-contrast + centered
// so it doesn't compete with nav. Links to the org's GitHub in a new tab.
function SidebarAttribution() {
  return (
    <a
      href="https://github.com/TheBranchDriftCatalyst"
      target="_blank"
      rel="noreferrer"
      className="mt-0.5 block text-center text-[10px] text-muted-foreground/70 hover:text-foreground"
      title="github.com/TheBranchDriftCatalyst"
    >
      Made by Catalyst Development
    </a>
  );
}
