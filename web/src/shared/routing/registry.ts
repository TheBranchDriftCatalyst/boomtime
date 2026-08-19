// Shared route-registration seam (the registry).
//
// A mutable, module-level registry the shell OWNS but never populates. Domains
// call registerRoute() from their own registration module; the host entry
// composes which domains register (see web/src/app/registerDomains.ts). The
// shell's router imports NO domain page — it renders whatever the registry
// holds — which is the inversion that lets an isolated books build drop every
// boomtime page module.
import type { RouteDef } from "./types";

// key -> def. Keyed by (parent, index|path) so re-registering the same slot
// replaces the prior entry — idempotent across HMR / test-setup re-import, and
// registration order never matters (final order comes from the `order` field).
const routes = new Map<string, RouteDef>();

function keyFor(r: RouteDef): string {
  const parent = r.parent ?? "";
  const leaf = r.index ? "\0index" : (r.path ?? "");
  return `${parent}\0${leaf}`;
}

/** Register (or replace) a single route. Idempotent by (parent, path|index). */
export function registerRoute(route: RouteDef): void {
  routes.set(keyFor(route), route);
}

function sortKey(o?: number): number {
  return o ?? Number.MAX_SAFE_INTEGER;
}

/** All registered routes, globally ordered by `order`. The router (App.tsx)
 *  buckets these into a nested tree by `parent`; a stable global sort means
 *  each sibling group comes out in `order`. */
export function getRoutes(): RouteDef[] {
  return Array.from(routes.values()).sort(
    (a, b) => sortKey(a.order) - sortKey(b.order),
  );
}

/** Test aid — clears the registry so a test can register a known fixture set. */
export function __resetRouteRegistry(): void {
  routes.clear();
}
