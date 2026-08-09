// Page — the shared Page Object Model (POM) primitive (fe-pom-shell).
//
// Status: LIVE. Leaderboards is the pilot (Phase A); the remaining feature
// pages migrate onto it in Phase B — see docs/design/fe-pom-shell-spike.md.
//
// Every feature page today hand-rolls the same skeleton:
//
//   <div>
//     <PageToolbar title="…">{range picker, widgets panel, …}</PageToolbar>
//     <div className="space-y-6"> …content… </div>
//   </div>
//
// `<Page>` formalizes that skeleton so composition is DRY and — critically for
// the no-scroll shell — so the CONTENT region is the single owner of vertical
// scroll. The shell sets the viewport to 100dvh and hides its own overflow;
// `<Page.Content>` is the one `overflow-y-auto` box, which keeps the toolbar
// (title + range picker) pinned while only the charts scroll.
//
// It EXTENDS catalyst-ui rather than forking it: the header slot renders the
// existing `<PageToolbar>` verbatim, so the title typography, action wrapping,
// and spacing stay identical to what ships today.
import type { ReactNode } from "react";
import { PageToolbar } from "@thebranchdriftcatalyst/catalyst-ui/components/PageToolbar";
import { cn } from "@/lib/utils";
// gaka-lzr: app-wide --grid-tile-* CSS vars for the isolated grid primitive
// (web/src/lib/grid), sourced from catalyst-ui theme tokens. `<Page>` is the
// shared shell every in-app dashboard renders through, so importing it here
// (rather than per-page) is the one place that reaches every consumer of
// the grid primitive inside the authed app.
import "./gridTheme.css";

export interface PageProps {
  children: ReactNode;
  /** Extra classes on the page root (rarely needed). */
  className?: string;
}

/** Page root — a full-height flex column that fills whatever the shell's
 * content cell gives it. `min-h-0` is load-bearing: without it a flex child
 * refuses to shrink below its content size and the inner `overflow-y-auto`
 * never engages (the whole shell scrolls instead). */
export function Page({ children, className }: PageProps) {
  return (
    <div
      data-testid="page"
      className={cn("flex h-full min-h-0 flex-col", className)}
    >
      {children}
    </div>
  );
}

export interface PageHeaderProps {
  /** Page heading (h1). Same contract as PageToolbar.title. */
  title: ReactNode;
  /** Optional muted secondary line under the title. */
  subtitle?: ReactNode;
  /** Right-aligned action slot — range picker, widgets panel, tab strip. */
  children?: ReactNode;
  className?: string;
}

/** Page header — thin wrapper over catalyst-ui's `<PageToolbar>`. Kept as a
 * named sub-part so callers write `<Page.Header>` uniformly and a future
 * change to the header chrome (e.g. a breadcrumb row, a sticky shadow on
 * scroll) lands in ONE place instead of ~11 pages. `shrink-0` keeps it out of
 * the scroll region. */
function PageHeader({ title, subtitle, children, className }: PageHeaderProps) {
  return (
    <PageToolbar
      data-testid="page-header"
      title={title}
      subtitle={subtitle}
      className={cn("mb-0 shrink-0 px-6 pt-6", className)}
    >
      {children}
    </PageToolbar>
  );
}

export interface PageBodyProps {
  children: ReactNode;
  /** Optional right rail. When present, content + aside sit side-by-side and
   * the aside is fixed-width on large viewports, stacking under content on
   * small ones. */
  aside?: ReactNode;
  className?: string;
}

/** Page body — the horizontal split between the main content column and an
 * optional aside rail. Fills the remaining height and hides its own overflow;
 * the scroll lives one level down in `<Page.Content>` so content and aside can
 * scroll independently. */
function PageBody({ children, aside, className }: PageBodyProps) {
  return (
    <div
      data-testid="page-body"
      className={cn("flex min-h-0 flex-1 gap-4", className)}
    >
      {children}
      {aside}
    </div>
  );
}

export interface PageContentProps {
  children: ReactNode;
  className?: string;
}

/** Page content — THE scroll region. In the no-scroll shell this is the only
 * box in the whole page tree with `overflow-y-auto`. `p-6` matches the padding
 * the current `<main>` applies so migrated pages look unchanged. */
function PageContent({ children, className }: PageContentProps) {
  return (
    <div
      data-testid="page-content"
      className={cn("min-h-0 flex-1 overflow-y-auto p-6 pt-4", className)}
    >
      {children}
    </div>
  );
}

export interface PageAsideProps {
  children: ReactNode;
  className?: string;
}

/** Page aside — optional right rail (e.g. a widget palette, a detail panel).
 * Scrolls independently of the content column. Fixed width on lg+, hidden on
 * narrow viewports by default (callers can override via className). */
function PageAside({ children, className }: PageAsideProps) {
  return (
    <aside
      data-testid="page-aside"
      className={cn(
        "hidden min-h-0 w-80 shrink-0 overflow-y-auto border-l p-4 lg:block",
        className,
      )}
    >
      {children}
    </aside>
  );
}

// Namespaced sub-parts so call sites read as one cohesive unit:
//   <Page>
//     <Page.Header title="Overview">{controls}</Page.Header>
//     <Page.Body aside={<Page.Aside>…</Page.Aside>}>
//       <Page.Content>…</Page.Content>
//     </Page.Body>
//   </Page>
Page.Header = PageHeader;
Page.Body = PageBody;
Page.Content = PageContent;
Page.Aside = PageAside;
