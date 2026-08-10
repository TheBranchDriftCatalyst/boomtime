import type { ComponentType, ReactNode } from "react";
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
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/tooltip";
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
    "flex items-center rounded-lg text-sm font-medium transition-colors",
    // Collapsed: a centered 40px square — a clean, intentional icon rail with a
    // proper ≥40px tap target (was a full-width stretched row with a tiny
    // icon). Expanded: full-width row with the label.
    collapsed ? "mx-auto h-10 w-10 justify-center" : "gap-3 px-3 py-2",
    isActive
      ? "bg-sidebar-primary text-sidebar-primary-foreground"
      : "text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground",
  );
}

/** RailTip — when the rail is collapsed, wrap an item in a styled tooltip that
 * flies out to the right showing its label (the icon-only rail is a mystery
 * without it). When expanded (or in the mobile drawer, where collapsed is
 * always false) it renders the child untouched — no tooltip, no wrapper. This
 * replaces the old native `title=` attributes, which were slow, unstyled, and
 * inconsistently positioned. */
function RailTip({
  collapsed,
  label,
  children,
}: {
  collapsed: boolean;
  label: string;
  children: ReactNode;
}) {
  if (!collapsed) return <>{children}</>;
  return (
    <Tooltip>
      <TooltipTrigger asChild>{children}</TooltipTrigger>
      <TooltipContent side="right" sideOffset={8}>
        {label}
      </TooltipContent>
    </Tooltip>
  );
}

/** NavItem — a single top-level nav destination. Owns the RailTip wrap, the
 * active styling, and calls onNavigate on click (the mobile drawer passes a
 * close-the-sheet callback here; the rail passes nothing). */
function NavItem({
  to,
  end,
  icon: Icon,
  name,
  collapsed,
  onNavigate,
  testId,
}: {
  to: string;
  end?: boolean;
  icon: ComponentType<{ className?: string }>;
  name: string;
  collapsed: boolean;
  onNavigate?: () => void;
  testId?: string;
}) {
  return (
    <RailTip collapsed={collapsed} label={name}>
      <NavLink
        to={to}
        end={end}
        aria-label={name}
        onClick={onNavigate}
        data-testid={testId}
        className={({ isActive }) => sidebarItemClass(collapsed, isActive)}
      >
        <Icon className="h-5 w-5 shrink-0" />
        {!collapsed && <span className="truncate">{name}</span>}
      </NavLink>
    </RailTip>
  );
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
  onNavigate,
  publicProfileSlot,
}: {
  collapsed: boolean;
  onCreateSpace: () => void;
  onNavigate?: () => void;
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
          <RailTip key={space.id} collapsed={collapsed} label={space.name}>
            <NavLink
              to={`/app/space/${space.id}`}
              aria-label={space.name}
              onClick={onNavigate}
              className={({ isActive }) => sidebarItemClass(collapsed, isActive)}
            >
              <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-sm bg-secondary text-[11px] font-semibold text-secondary-foreground">
                {initial}
              </span>
              {!collapsed && <span className="truncate">{space.name}</span>}
            </NavLink>
          </RailTip>
        );
      })}

      <RailTip collapsed={collapsed} label="New space">
        <button
          onClick={() => {
            onNavigate?.();
            onCreateSpace();
          }}
          aria-label="New space"
          className={cn(!collapsed && "w-full", sidebarItemClass(collapsed, false))}
        >
          <Plus className="h-5 w-5 shrink-0" />
          {!collapsed && "New space"}
        </button>
      </RailTip>
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
function ProfileNavLink({
  collapsed,
  onNavigate,
}: {
  collapsed: boolean;
  onNavigate?: () => void;
}) {
  return (
    <NavItem
      to="/app/profile"
      icon={UserCircle}
      name="Profile"
      collapsed={collapsed}
      onNavigate={onNavigate}
      testId="sidebar-public-profile"
    />
  );
}

/** AdminNavLink — top-level Admin section entry (gaka-ebq). Rendered only
 * when the current user is on BOOM_ADMIN_USERS. Matches active on any
 * /app/admin descendant so a sub-tab (labels/backfill/logs) still lights
 * up the parent link. Hidden entirely (not disabled, not "unauthorized")
 * for non-admins — same visual model as PublicProfileNavLink: if it isn't
 * yours, it isn't in the sidebar. */
function AdminNavLink({
  collapsed,
  onNavigate,
}: {
  collapsed: boolean;
  onNavigate?: () => void;
}) {
  const { isAdmin, isLoading } = useIsAdmin();
  // Render nothing during the first-paint auth check so we don't flash a
  // link in for admins-of-record who reload. The Overview page is happy
  // to render behind us regardless.
  if (isLoading || !isAdmin) return null;

  return (
    <NavItem
      to="/app/admin"
      icon={ShieldCheck}
      name="Admin"
      collapsed={collapsed}
      onNavigate={onNavigate}
      testId="sidebar-admin"
    />
  );
}

interface SidebarBodyProps {
  collapsed: boolean;
  onLogout: () => void;
  onCreateSpace: () => void;
  /** Called after any nav destination is clicked — the mobile drawer passes a
   * close-the-sheet callback so tapping a link dismisses the drawer. */
  onNavigate?: () => void;
  /** The collapse/expand toggle only makes sense on the desktop rail; the
   * mobile drawer hides it (there's no rail to collapse to). */
  onToggleCollapsed?: () => void;
  showCollapseToggle?: boolean;
}

/** SidebarBody — the brand + nav + footer, shared verbatim by the desktop rail
 * (`Sidebar`) and the mobile drawer (`MobileNav`) so the two never drift. The
 * outer container (the <aside> or the <SheetContent>) owns width + background
 * and lays this out as a flex column. */
export function SidebarBody({
  collapsed,
  onLogout,
  onCreateSpace,
  onNavigate,
  onToggleCollapsed,
  showCollapseToggle = true,
}: SidebarBodyProps) {
  return (
    <>
      <div
        className={cn(
          "flex h-16 shrink-0 items-center border-b",
          collapsed ? "justify-center px-0" : "gap-2 px-6",
        )}
      >
        <img
          src="/boomtime.svg"
          alt=""
          aria-hidden="true"
          className="h-8 w-8 shrink-0 rounded-lg"
        />
        {!collapsed && <span className="text-lg font-semibold">Boomtime</span>}
      </div>

      <nav className="min-h-0 flex-1 space-y-1 overflow-y-auto p-3">
        {NAV.map((item) => (
          <NavItem
            key={item.to}
            to={item.to}
            end={item.end}
            icon={item.icon}
            name={item.name}
            collapsed={collapsed}
            onNavigate={onNavigate}
          />
        ))}

        <AdminNavLink collapsed={collapsed} onNavigate={onNavigate} />

        {/* Profile lives in the Spaces group — it's semantically a "space" too
            (a scoped, publishable view of your data). Order: Profile first,
            then user-created Spaces, then New space. */}
        <SpacesNavGroup
          collapsed={collapsed}
          onCreateSpace={onCreateSpace}
          onNavigate={onNavigate}
          publicProfileSlot={
            <ProfileNavLink collapsed={collapsed} onNavigate={onNavigate} />
          }
        />
      </nav>

      <div className="shrink-0 space-y-1 border-t p-3">
        <RailTip collapsed={collapsed} label="Logout">
          <button
            onClick={onLogout}
            aria-label="Logout"
            className={cn(!collapsed && "w-full", sidebarItemClass(collapsed, false))}
          >
            <LogOut className="h-5 w-5 shrink-0" />
            {!collapsed && "Logout"}
          </button>
        </RailTip>

        {showCollapseToggle && onToggleCollapsed && (
          <RailTip
            collapsed={collapsed}
            label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          >
            <button
              onClick={onToggleCollapsed}
              aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
              aria-expanded={!collapsed}
              className={cn(!collapsed && "w-full", sidebarItemClass(collapsed, false))}
            >
              {collapsed ? (
                <PanelLeftOpen className="h-5 w-5 shrink-0" />
              ) : (
                <PanelLeftClose className="h-5 w-5 shrink-0" />
              )}
              {!collapsed && "Collapse"}
            </button>
          </RailTip>
        )}

        {!collapsed && (
          <>
            <SidebarVersion />
            <SidebarAttribution />
          </>
        )}
      </div>
    </>
  );
}

interface SidebarProps {
  collapsed: boolean;
  onToggleCollapsed: () => void;
  onLogout: () => void;
  onCreateSpace: () => void;
}

/** App sidebar: the desktop icon-collapsible rail. Wraps the shared SidebarBody
 * in a TooltipProvider so the collapsed rail's fly-out labels work. Hidden
 * below md — the mobile nav is the Sheet drawer in HeaderBar (MobileNav). */
export function Sidebar({
  collapsed,
  onToggleCollapsed,
  onLogout,
  onCreateSpace,
}: SidebarProps) {
  return (
    <TooltipProvider delayDuration={0}>
      <aside
        className={cn(
          "hidden shrink-0 flex-col border-r bg-sidebar text-sidebar-foreground transition-[width] duration-200 ease-in-out md:flex",
          collapsed ? "w-16" : "w-60",
        )}
      >
        <SidebarBody
          collapsed={collapsed}
          onToggleCollapsed={onToggleCollapsed}
          onLogout={onLogout}
          onCreateSpace={onCreateSpace}
        />
      </aside>
    </TooltipProvider>
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
