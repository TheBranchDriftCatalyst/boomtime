// SectionRail — the vertical, domain-grouped sub-navigation for a section
// shell (Admin, Settings) (gaka-4x33).
//
// It replaces the grouped tab strip those two sections used to hoist into the
// app HeaderBar. That strip failed for two independent reasons:
//
//   1. STRUCTURAL. Its intrinsic width stretched the shell's content grid
//      column past the viewport, and the shell's overflow-hidden clipped the
//      header's right-side controls (search, notifications, avatar, logout)
//      permanently out of reach. See AppShellNoScroll's gaka-c26s note. That
//      specific mechanism is fixed at the shell now, but the pressure that
//      caused it — an unbounded, domain-extensible list competing with fixed
//      chrome for one 64px row — is intrinsic to putting section nav up there.
//
//   2. LEGIBILITY. Group labels and tabs shared a row and near-identical type,
//      so "BOOMTIME" (a label) read as clickable while "Labels" (a tab) did
//      not. Nine tabs under three group labels is a list, not a tab strip.
//
// A vertical rail is the shape that actually fits the data: groups get their
// own line, labels can't be mistaken for targets, and the list grows downward
// (a direction the layout has to spare) as domains register more.
//
// DOMAIN-FREE, like the rest of `@shared/layout`: no feature imports, no
// registry imports, no router coupling beyond react-router's NavLink. The
// caller resolves its registry and hands over plain items — which is what lets
// Admin (route-driven, `to`) and Settings (?tab=-driven, `onSelect`) share one
// implementation.
import type { ComponentType, ReactNode } from "react";
import { NavLink } from "react-router";
import { ExternalLink } from "lucide-react";
import { cn } from "@shared/lib/utils";

export interface SectionRailItem {
  /** stable key. */
  id: string;
  label: ReactNode;
  /** optional leading glyph — the registry's per-tab icon. */
  icon?: ComponentType<{ className?: string }>;
  /**
   * Route target. Renders a NavLink whose active state comes from the URL.
   * Mutually exclusive with `onSelect`.
   */
  to?: string;
  /**
   * Click handler. Renders a button and the caller drives `active` itself —
   * for sections that switch on state or a query param rather than a route.
   */
  onSelect?: () => void;
  /** Active state for `onSelect` items. Ignored for `to` items (the URL wins). */
  active?: boolean;
  /**
   * `to` addresses something OUTSIDE the SPA (the Swagger UI at /api/docs/).
   * Renders a plain anchor into a new tab: the router never sees the path, so
   * it can't 404 on one the SPA doesn't own, and it is never the active item.
   */
  external?: boolean;
  /** optional trailing content — a count, a status dot, a live badge. */
  badge?: ReactNode;
}

export interface SectionRailGroup {
  /** stable key — a domain key ("catalystbooks", "boomtime") or "core". */
  id: string;
  /** uppercase group header; omit for an unlabeled (core) cluster. */
  label?: ReactNode;
  items: SectionRailItem[];
}

export interface SectionRailProps {
  /** Accessible name for the nav landmark, e.g. "Admin sections". */
  ariaLabel: string;
  groups: SectionRailGroup[];
  className?: string;
}

function itemClass(isActive: boolean): string {
  return cn(
    "flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left text-sm transition-colors",
    // Mono/uppercase matches the admin visual language the tab strip
    // established, so this reads as the same surface reorganized rather than a
    // different app.
    "font-mono text-xs uppercase tracking-wider",
    isActive
      ? "bg-sidebar-primary/15 font-semibold text-primary"
      : "text-muted-foreground hover:bg-foreground/[0.06] hover:text-foreground",
  );
}

/**
 * The rail. Renders one `<nav>` landmark containing a labeled list per group.
 *
 * Sizing is the CALLER's business (this returns a plain block element) — Admin
 * and Settings mount it through `<Page.Body nav={…}>`, which owns the fixed
 * width, the divider, and the independent scroll.
 */
export function SectionRail({ ariaLabel, groups, className }: SectionRailProps) {
  return (
    <nav aria-label={ariaLabel} className={cn("space-y-5", className)}>
      {groups.map((group) => (
        <div key={group.id} className="space-y-1">
          {group.label != null && (
            // Not a heading element on purpose: these label sibling clusters
            // inside one nav landmark, and promoting each to an <h*> would
            // inject a phantom outline level between the page title and the
            // tab body's own headings.
            <div className="px-3 pb-1 font-mono text-[10px] uppercase tracking-[0.18em] text-muted-foreground/70">
              {group.label}
            </div>
          )}
          {group.items.map((item) => {
            const Icon = item.icon;
            const inner = (
              <>
                {Icon && <Icon className="size-3.5 shrink-0" />}
                <span className="min-w-0 flex-1 truncate">{item.label}</span>
                {item.badge}
                {item.external && (
                  <ExternalLink className="size-3 shrink-0" aria-hidden="true" />
                )}
              </>
            );

            if (item.external && item.to) {
              return (
                <a
                  key={item.id}
                  href={item.to}
                  target="_blank"
                  rel="noreferrer"
                  className={itemClass(false)}
                >
                  {inner}
                </a>
              );
            }
            if (item.to) {
              return (
                <NavLink
                  key={item.id}
                  to={item.to}
                  end
                  className={({ isActive }) => itemClass(isActive)}
                >
                  {inner}
                </NavLink>
              );
            }
            return (
              <button
                key={item.id}
                type="button"
                onClick={item.onSelect}
                aria-current={item.active ? "page" : undefined}
                className={itemClass(Boolean(item.active))}
              >
                {inner}
              </button>
            );
          })}
        </div>
      ))}
    </nav>
  );
}
