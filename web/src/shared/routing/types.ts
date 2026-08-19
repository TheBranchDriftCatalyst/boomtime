// Shared route-registration seam (types).
//
// Same dependency-inversion discipline as the nav / settings / admin seams:
// the shell OWNS the registry but registers NO domain route itself. Each domain
// pushes its own routes in (books routes in domains/books, boomtime routes in
// domains/boomtime, the shared/core routes — Overview / Settings / Admin shell /
// Profile / auth — in domains/core). The shell's router (App.tsx) builds its
// `<Routes>` tree purely from what has been registered, so a standalone books
// build (which never registers the boomtime domain) tree-shakes every boomtime
// page module away — the router table itself no longer names them.
import type { ReactNode } from "react";

/** One registered route. Routes nest via `parent` referencing another route's
 *  `id` — this reproduces the `<Route>`-inside-`<Route>` tree (e.g. the /app
 *  shell wrapper and the /app/admin sub-shell) without any single module having
 *  to see every domain's leaves. Elements are pre-built by the registering
 *  domain (already wrapped in <Suspense>, given props, etc.), so the shell just
 *  drops them into the tree verbatim. */
export interface RouteDef {
  /** stable id — only required when other routes nest under this one
   *  (a layout/wrapper route like the "/app" shell or "admin" sub-shell). */
  id?: string;
  /** id of the parent route this nests under; omit for a top-level route. */
  parent?: string;
  /** route path (relative to the parent, react-router style). Omit for an
   *  index route (set `index: true` instead). */
  path?: string;
  /** true for the parent's index route (`<Route index>`). */
  index?: boolean;
  /** the element rendered at this route — already Suspense-wrapped / prop-bound
   *  by the registering domain. */
  element: ReactNode;
  /** sort key among siblings (ascending; unset sorts last, stable). Ordering is
   *  cosmetic — react-router ranks by path specificity, not declaration order —
   *  but a stable order keeps the built tree deterministic. */
  order?: number;
}
