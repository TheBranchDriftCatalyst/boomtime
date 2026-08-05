// AppShellNoScroll — the no-scroll shell layout (fe-pom-shell Phase A).
//
// Wired: `@/layout/AppShell` composes this with the real Sidebar / HeaderBar /
// dialogs and the router <Outlet/>. See docs/design/fe-pom-shell-spike.md.
//
// The problem with the previous shell:
//
//   <div className="flex h-full min-h-screen">      // grows with content
//     <Sidebar/>
//     <div className="flex flex-1 flex-col overflow-hidden">
//       <HeaderBar/>
//       <main className="flex-1 overflow-y-auto p-6">{Outlet}</main>
//     </div>
//   </div>
//
// `min-h-screen` lets the whole shell GROW past the viewport, so on tall pages
// the body itself scrolls and the sidebar/header scroll away with it. This
// variant pins the shell to exactly one viewport (100dvh) via a CSS grid and
// hands ALL vertical scroll to the page's `<Page.Content>` region. The sidebar
// and header never move.
//
// It is intentionally SLOT-BASED (sidebar / header / children props) rather
// than re-importing the auth hooks + dialogs itself. AppShell owns that wiring
// and passes the composed Sidebar / HeaderBar / <Outlet/> in; this file stays a
// pure layout primitive.
import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export interface AppShellNoScrollProps {
  /** Left rail. Spans both grid rows (sits beside header + content). */
  sidebar: ReactNode;
  /** Top bar. Row 1 of the content column; never scrolls. */
  header: ReactNode;
  /** Main content cell. Row 2; `overflow-hidden` — scrolling is delegated to
   * the `<Page.Content>` inside it. In live routes this is `<Outlet/>`. */
  children: ReactNode;
  className?: string;
}

/**
 * The shell owns the viewport. `h-dvh` (dynamic viewport height) beats `h-screen`
 * on mobile — it excludes the collapsing browser chrome so the grid never
 * overflows behind the URL bar. `overflow-hidden` on the root guarantees the
 * body never scrolls; `min-h-0` on the content cell lets its inner
 * `overflow-y-auto` engage.
 *
 * Grid:
 *   cols = [sidebar auto][content 1fr]
 *   rows = [header auto][content minmax(0,1fr)]   // minmax(0,…) == min-h-0
 *   sidebar spans both rows; header + main stack in the content column.
 */
export function AppShellNoScroll({
  sidebar,
  header,
  children,
  className,
}: AppShellNoScrollProps) {
  return (
    <div
      className={cn(
        "grid h-dvh grid-cols-[auto_1fr] grid-rows-[auto_minmax(0,1fr)] overflow-hidden bg-background",
        className,
      )}
    >
      {/* Sidebar — column 1, both rows. The wrapper is a flex box with
          min-h-0 so the sidebar <aside> stretches to the full grid height the
          way it did as a direct flex child of the old shell (its `flex-1` nav
          + footer depend on a definite parent height). When the sidebar is
          hidden (`hidden md:flex` on mobile) the `auto` column collapses to
          zero width and content spans the row. */}
      <div className="row-span-2 flex min-h-0">{sidebar}</div>

      {/* Header — content column, row 1. */}
      <div className="col-start-2 row-start-1">{header}</div>

      {/* Main — content column, row 2. Hides its own overflow; the page's
          <Page.Content> is the single scroll owner. */}
      <main className="col-start-2 row-start-2 min-h-0 overflow-hidden">
        {children}
      </main>
    </div>
  );
}
