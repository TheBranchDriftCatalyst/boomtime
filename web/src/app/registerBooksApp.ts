// STANDALONE catalyst-books composition root (gaka-zp2s) — the ONE place the
// books-only app decides which domains register their FE surface. The mirror of
// web/src/app/registerDomains.ts (the host), but composing a LEANER set:
//
//   - the HOST app registers core + books + boomtime (every nav entry, settings
//     tab, admin tab, and route).
//   - THIS standalone app registers ONLY core + books. registerBoomtimeDomain
//     is never imported here, so the bundler drops every code-domain page
//     module (Projects / Leaderboards / Heartbeats / Wellness / …) — the router
//     table no longer names them, and the shared shell imports no domain, so
//     tree-shaking is total.
//
// Same shell, same providers, same theme as the host (see books-main.tsx) —
// only the registered domains differ.
import { registerCoreDomain } from "@/domains/core/register";
import { registerBooksDomain } from "@/domains/books/register";

let done = false;

/** Register the domains the standalone books app ships: core (shared shell +
 *  auth/public routes + Overview/Settings/Admin) + books. Idempotent — safe to
 *  call from the app entry (the underlying register* calls are themselves
 *  idempotent, and this guard skips repeat work). */
export function registerBooksAppDomains(): void {
  if (done) return;
  done = true;
  registerCoreDomain();
  registerBooksDomain();
}
