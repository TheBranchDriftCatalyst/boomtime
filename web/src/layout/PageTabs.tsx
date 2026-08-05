// PageTabs — the shared horizontal tab strip (fe-pom-shell Phase B).
//
// Admin (NavLink child-routes) and Settings (?tab= buttons) each hand-rolled
// the SAME strip: a bottom-bordered row whose active item gets a primary-color
// underline (`-mb-px border-b-2 border-primary`). Two copies of one visual.
// This unifies the container + the active/inactive underline logic into one
// place; callers still supply their own tab elements (<NavLink> for routed
// tabs, <button> for state tabs) and their own label typography via `extra`,
// so neither surface changes appearance — only the duplication goes away.
import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export interface PageTabStripProps {
  /** Accessible name for the tablist (e.g. "Admin sections"). */
  ariaLabel: string;
  /** The tab items — <NavLink role="tab"> or <button role="tab">. */
  children: ReactNode;
  className?: string;
}

/** The tablist container: a full-width row with a bottom border the active
 * tab's underline sits flush against (via each item's `-mb-px`). */
export function PageTabStrip({ ariaLabel, children, className }: PageTabStripProps) {
  return (
    <div
      role="tablist"
      aria-label={ariaLabel}
      className={cn("mb-6 flex gap-1 border-b border-border", className)}
    >
      {children}
    </div>
  );
}

/** Per-tab class. `extra` carries each surface's own typography (Admin uses a
 * mono uppercase treatment; Settings a plain medium one) so unifying the strip
 * doesn't flatten their distinct looks. */
export function pageTabClass(isActive: boolean, extra?: string): string {
  return cn(
    "-mb-px border-b-2 px-4 py-2 transition-colors",
    isActive
      ? "border-primary text-foreground"
      : "border-transparent text-muted-foreground hover:border-border hover:text-foreground",
    extra,
  );
}
