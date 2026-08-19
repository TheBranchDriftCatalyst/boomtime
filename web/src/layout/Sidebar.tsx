import { useState, type ComponentType, type ReactNode } from "react";
import { NavLink } from "react-router";
import { useQuery } from "@tanstack/react-query";
import {
  LogOut,
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  ShieldCheck,
  UserCircle,
} from "lucide-react";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/tooltip";
import { useSpaces } from "@boomtime/features/spaces/useSpaces";
import { useIsAdmin } from "@/features/auth/useIsAdmin";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { usePublicConfig } from "@/lib/usePublicConfig";
import { resolveNavSections } from "@/shared/nav/registry";
import type { NavSection } from "@/shared/nav/types";
import { cn } from "@/lib/utils";
import { IS_BOOKS_STANDALONE, STANDALONE_APP_NAME } from "@/lib/standalone";

// Single source for the sidebar item styling (nav links, space links, and the
// action buttons all share it; buttons pass isActive=false and add w-full).
function sidebarItemClass(collapsed: boolean, isActive: boolean): string {
  return cn(
    "flex items-center text-sm font-medium transition-colors",
    // Collapsed: a centered 40px rounded-xl tile — a clean, premium icon rail
    // (Linear/Vercel-style) with a proper ≥40px tap target. Expanded: full-width
    // row with the label.
    collapsed ? "mx-auto h-10 w-10 justify-center rounded-xl" : "gap-3 rounded-lg px-3 py-2",
    isActive
      ? collapsed
        ? // Collapsed active: a restrained magenta-tinted fill + magenta glyph.
          // The crisp neon hairline + outer glow is layered on for free by the
          // shipped global rule `.theme-boomtime.dark a[aria-current="page"]`
          // (catalyst-ui boomtime.css) — no loud solid neon block.
          "bg-sidebar-primary/15 text-sidebar-primary"
        : // Expanded active: unchanged — solid primary fill + halo.
          "bg-sidebar-primary text-sidebar-primary-foreground"
      : collapsed
        ? // Collapsed inactive: a subtle white lift on hover (the old full-cyan
          // sidebar-accent fill was far too loud for a dense icon rail).
          "text-muted-foreground hover:bg-foreground/[0.07] hover:text-sidebar-foreground"
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

/** NavSectionHeader — the uppercase group label above a domain's nav cluster.
 * Mirrors the Spaces group header so grouped domain sections and Spaces read as
 * one system. Collapsed rail shows a hairline divider instead of the text. */
function NavSectionHeader({
  label,
  collapsed,
}: {
  label: string;
  collapsed: boolean;
}) {
  if (collapsed) {
    return <div className="mx-1.5 my-2 border-t border-sidebar-border" />;
  }
  return (
    <div className="flex items-center justify-between px-3 pb-1 pt-3">
      <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        {label}
      </span>
    </div>
  );
}

/** NavSectionGroup — one registered domain section: an optional header followed
 * by its nav items. Unlabeled sections (core / config) render their items flat,
 * with no header, so Overview + Settings sit at the top level while the code
 * pages read as a grouped "Boomtime" domain. */
function NavSectionGroup({
  section,
  collapsed,
  onNavigate,
}: {
  section: NavSection;
  collapsed: boolean;
  onNavigate?: () => void;
}) {
  return (
    <div className="space-y-1">
      {section.label && (
        <NavSectionHeader label={section.label} collapsed={collapsed} />
      )}
      {section.items.map((item) => (
        <NavItem
          key={item.to}
          to={item.to}
          end={item.end}
          icon={item.icon}
          name={item.name}
          collapsed={collapsed}
          onNavigate={onNavigate}
          testId={item.testId}
        />
      ))}
    </div>
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
        <div className="mx-1.5 my-2 border-t border-sidebar-border" />
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
              {collapsed ? (
                // Collapsed: render the initial as a plain glyph so its visual
                // weight matches the lucide nav icons (the old filled mini-tile
                // clashed with the outline icons on the rail). It inherits the
                // item's text color — muted normally, magenta when active.
                <span className="text-[15px] font-bold leading-none tracking-tight">
                  {initial}
                </span>
              ) : (
                // Expanded: keep the filled secondary badge beside the label.
                <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-sm bg-secondary text-[11px] font-semibold text-secondary-foreground">
                  {initial}
                </span>
              )}
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
  // Nav is assembled from the shared nav-registration seam (each domain pushes
  // its entries in via registerNavItem). resolveNavSections drops flag-gated
  // items whose public-config flag is off — currently just Books on books_enabled
  // — so a disabled feature's nav is fully inert. Flags default false while the
  // config request is in flight.
  const { config } = usePublicConfig();
  const navSections = resolveNavSections(config);

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
        {!collapsed && (
          <span className="text-lg font-semibold">
            {IS_BOOKS_STANDALONE ? STANDALONE_APP_NAME : "Boomtime"}
          </span>
        )}
      </div>

      <nav className="min-h-0 flex-1 space-y-1 overflow-y-auto p-3">
        {navSections.map((section) => (
          <NavSectionGroup
            key={section.id}
            section={section}
            collapsed={collapsed}
            onNavigate={onNavigate}
          />
        ))}

        {/* Admin + Spaces are HOST-ONLY. The books-only standalone is a
            single-local-user app: no admin console, no user-created Spaces
            (and no /spaces fetch — see useSpaces). Its nav is Books + Settings
            + Profile ONLY, so we render the lone Profile link directly instead
            of the Spaces group that would otherwise host it. */}
        {IS_BOOKS_STANDALONE ? (
          <div className="pt-4">
            <ProfileNavLink collapsed={collapsed} onNavigate={onNavigate} />
          </div>
        ) : (
          <>
            <AdminNavLink collapsed={collapsed} onNavigate={onNavigate} />

            {/* Profile lives in the Spaces group — it's semantically a "space"
                too (a scoped, publishable view of your data). Order: Profile
                first, then user-created Spaces, then New space. */}
            <SpacesNavGroup
              collapsed={collapsed}
              onCreateSpace={onCreateSpace}
              onNavigate={onNavigate}
              publicProfileSlot={
                <ProfileNavLink collapsed={collapsed} onNavigate={onNavigate} />
              }
            />
          </>
        )}
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
  // Hover-to-peek (gaka-k26n.11): when collapsed, hovering the rail expands it
  // to full labels. The OUTER wrapper keeps the collapsed column width, and the
  // <aside> overlays (absolute + z-50) so page content never shifts during the
  // peek — it slides back the moment the pointer leaves.
  const [peeking, setPeeking] = useState(false);
  const expanded = !collapsed || peeking;

  return (
    <div
      className={cn(
        "relative hidden shrink-0 md:block",
        collapsed ? "w-16" : "w-60",
      )}
    >
      <TooltipProvider delayDuration={0}>
        <aside
          onMouseEnter={() => collapsed && setPeeking(true)}
          onMouseLeave={() => setPeeking(false)}
          className={cn(
            "absolute inset-y-0 left-0 flex flex-col overflow-hidden border-r bg-sidebar text-sidebar-foreground transition-[width] duration-200 ease-in-out",
            expanded ? "w-60" : "w-16",
            peeking && "z-50 shadow-2xl shadow-black/40",
          )}
        >
          <SidebarBody
            collapsed={!expanded}
            onToggleCollapsed={onToggleCollapsed}
            onLogout={onLogout}
            onCreateSpace={onCreateSpace}
          />
        </aside>
      </TooltipProvider>
    </div>
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
