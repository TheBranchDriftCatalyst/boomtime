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
import { cn } from "@shared/lib/utils";
// boom-lzr: app-wide --grid-tile-* CSS vars for the isolated grid primitive
// (web/shared/lib/grid), sourced from catalyst-ui theme tokens. `<Page>` is the
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
      className={cn("mb-0 shrink-0 px-4 pt-4 sm:px-6 sm:pt-6", className)}
    >
      {children}
    </PageToolbar>
  );
}

export interface PageBodyProps {
  children: ReactNode;
  /** Optional LEFT rail — section sub-navigation (see `<SectionRail>`). Fixed
   * width on md+, and on narrow viewports it stacks ABOVE the content so the
   * nav stays the first thing you reach. Distinct from `aside`, which is
   * supplementary and hides on small screens; nav is never optional. */
  nav?: ReactNode;
  /** Optional right rail. When present, content + aside sit side-by-side and
   * the aside is fixed-width on large viewports, stacking under content on
   * small ones. */
  aside?: ReactNode;
  className?: string;
}

/** Page body — the horizontal split between an optional nav rail, the main
 * content column, and an optional aside rail. Fills the remaining height and
 * hides its own overflow; the scroll lives one level down in `<Page.Content>`
 * so each column scrolls independently. */
function PageBody({ children, nav, aside, className }: PageBodyProps) {
  return (
    <div
      data-testid="page-body"
      className={cn(
        "flex min-h-0 flex-1 gap-4",
        // With a nav rail the body stacks on narrow viewports (rail on top,
        // content under) and becomes a row at md+. Without one the old
        // always-row behavior is preserved byte-for-byte.
        nav && "flex-col md:flex-row md:gap-0",
        className,
      )}
    >
      {nav}
      {children}
      {aside}
    </div>
  );
}

export interface PageNavProps {
  children: ReactNode;
  className?: string;
}

/** Page nav — the left rail container. Owns width, divider, padding, and its
 * OWN scroll, so a section with more entries than fit never pushes the page
 * (or, as the header tab strip once did, the shell) out of shape. `shrink-0`
 * plus the `minmax(0,1fr)` content column means a long label truncates inside
 * the rail instead of widening it.
 *
 * On mobile the rail stacks ABOVE the content, where an unbounded list would
 * push the page body off the first screen entirely — a 9-entry admin rail ate
 * ~500px before you reached anything. `max-h-56` there turns it into its own
 * short scroller so the content is always visible under it; from md up the cap
 * lifts and the rail runs full height beside the body. */
function PageNav({ children, className }: PageNavProps) {
  return (
    <div
      data-testid="page-nav"
      className={cn(
        "min-h-0 max-h-56 shrink-0 overflow-y-auto px-3 py-4",
        "md:max-h-none md:w-56 md:border-r md:px-4",
        className,
      )}
    >
      {children}
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
      className={cn("min-h-0 flex-1 overflow-y-auto p-4 pt-3 sm:p-6 sm:pt-4", className)}
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
//     <Page.Body
//       nav={<Page.Nav><SectionRail …/></Page.Nav>}   // optional left rail
//       aside={<Page.Aside>…</Page.Aside>}            // optional right rail
//     >
//       <Page.Content>…</Page.Content>
//     </Page.Body>
//   </Page>
Page.Header = PageHeader;
Page.Body = PageBody;
Page.Nav = PageNav;
Page.Content = PageContent;
Page.Aside = PageAside;
