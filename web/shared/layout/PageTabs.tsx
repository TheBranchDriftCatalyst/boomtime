// PageTabs / TabNav — the shared horizontal tab-navigation primitive.
//
// DOMAIN-FREE (mirrors `@shared/lib/grid`): imports only `cn` + a portable
// stylesheet; no `@shared/features`, no boomtime types, no router coupling. All
// neon/glow styling lives in TabNav.css reading standard theme tokens
// (--primary, --foreground, --muted-foreground, --border, --ring), so this
// pair can graduate to catalyst-ui unchanged. Router / ?tab= wiring stays in
// the pages that consume it.
//
// Two variants off one design:
//   variant="header"  compact, full-height, flush inside the app HeaderBar
//                     (hoisted via useHeaderSlot to reclaim the page's title row)
//   variant="page"    a standalone strip with a baseline border + bottom margin
//
// The active tab gets uppercase primary text with a neon glow and a bright,
// glowing 2px underline; inactive tabs are muted and fade an underline in on
// hover. See TabNav.css for the full treatment.
import "./TabNav.css";
import type { ReactNode } from "react";
import { cn } from "@shared/lib/utils";

export type TabNavVariant = "header" | "page";

export interface TabNavProps {
  /** Accessible name for the tablist (e.g. "Settings sections"). */
  ariaLabel: string;
  /** The tab items — <button role="tab"> or <NavLink role="tab">. Style each
   *  with {@link tabClass}. */
  children: ReactNode;
  /** "page" (default) or "header" (compact, app-bar-optimized). */
  variant?: TabNavVariant;
  /** Optional context prefix rendered before the tabs and OUTSIDE the tablist
   *  (kept out for a11y). Uppercased by CSS — pass "Settings", get "SETTINGS". */
  label?: ReactNode;
  className?: string;
}

/** The tab-navigation container. Renders an optional context label followed by
 *  a role="tablist" holding the caller's tabs. */
export function TabNav({
  ariaLabel,
  children,
  variant = "page",
  label,
  className,
}: TabNavProps) {
  return (
    <div
      className={cn(
        "catalyst-tabnav",
        variant === "header" ? "catalyst-tabnav--header" : "catalyst-tabnav--page",
        className,
      )}
    >
      {label != null && <span className="catalyst-tabnav__label">{label}</span>}
      <div role="tablist" aria-label={ariaLabel} className="catalyst-tabnav__list">
        {children}
      </div>
    </div>
  );
}

/** Per-tab class. Adds the active modifier when `isActive`; `extra` appends any
 *  caller-specific classes (rarely needed — typography is baked into the CSS). */
export function tabClass(isActive: boolean, extra?: string): string {
  return cn("catalyst-tab", isActive && "catalyst-tab--active", extra);
}

// ── Grouped variant ─────────────────────────────────────────────────────────
// GroupedTabNav lays out several labeled tab CLUSTERS in one row, each its own
// role="tablist" with a small uppercase group header + a divider between groups.
// It's the primitive the domain-grouped Settings + Admin strips render through:
// one visual strip, but the tabs are visibly bucketed by domain (Account /
// CatalystBooks / Boomtime …) instead of one flat list. Still DOMAIN-FREE — the
// caller supplies the groups + their tab nodes.

export interface TabNavGroup {
  /** stable key. */
  id: string;
  /** uppercase group header; omit for an unlabeled (core) cluster. */
  label?: ReactNode;
  /** the group's tab nodes — <button role="tab"> / <NavLink role="tab">. */
  children: ReactNode;
}

export interface GroupedTabNavProps {
  /** accessible base name; each group's tablist appends its label. */
  ariaLabel: string;
  groups: TabNavGroup[];
  variant?: TabNavVariant;
  /** optional context prefix before the first group (e.g. "Settings"). */
  label?: ReactNode;
  className?: string;
}

export function GroupedTabNav({
  ariaLabel,
  groups,
  variant = "header",
  label,
  className,
}: GroupedTabNavProps) {
  return (
    <div
      className={cn(
        "catalyst-tabnav catalyst-tabnav--grouped",
        variant === "header"
          ? "catalyst-tabnav--header"
          : "catalyst-tabnav--page",
        className,
      )}
    >
      {label != null && <span className="catalyst-tabnav__label">{label}</span>}
      {groups.map((g, i) => (
        <div key={g.id} className="catalyst-tabnav__group">
          {i > 0 && (
            <span aria-hidden="true" className="catalyst-tabnav__group-divider" />
          )}
          {g.label != null && (
            <span className="catalyst-tabnav__group-label">{g.label}</span>
          )}
          <div
            role="tablist"
            aria-label={g.label != null ? `${ariaLabel}: ${g.label}` : ariaLabel}
            className="catalyst-tabnav__list"
          >
            {g.children}
          </div>
        </div>
      ))}
    </div>
  );
}

// ── Back-compat aliases ─────────────────────────────────────────────────────
// Older call sites used <PageTabStrip>/pageTabClass (the flat underlined page
// strip). They now resolve to the "page" TabNav variant so the page treatment
// still works while everything shares one implementation.

export interface PageTabStripProps {
  ariaLabel: string;
  children: ReactNode;
  className?: string;
}

/** @deprecated Use `<TabNav variant="page">`. */
export function PageTabStrip({ ariaLabel, children, className }: PageTabStripProps) {
  return (
    <TabNav ariaLabel={ariaLabel} variant="page" className={className}>
      {children}
    </TabNav>
  );
}

/** @deprecated Use {@link tabClass}. */
export function pageTabClass(isActive: boolean, extra?: string): string {
  return tabClass(isActive, extra);
}
