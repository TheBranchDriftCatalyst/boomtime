/**
 * Dev utilities flags (boomtime fork of catalyst-ui/lib/dev).
 *
 * In boomtime the devtools (annotation subsystem + component inspector) are
 * gated on the current user being an admin — see `useIsAdmin()` and the
 * admin-only render of <DevModeToggle/> in `layout/HeaderBar.tsx`. These
 * helpers only drive dev-console logging and MUST NOT be used as the sole
 * visibility gate for admins in production (the toggle itself is admin-gated).
 */

/**
 * Whether dev-console logging inside the annotation subsystem is enabled.
 * Only true in a genuine `yarn dev` build; never in production.
 */
export function isDevUtilsEnabled(): boolean {
  return import.meta.env.DEV === true;
}

/**
 * Whether backend sync (file writes / POST) is allowed.
 *
 * ALWAYS false in boomtime: annotations are localStorage-only. The upstream
 * catalyst-ui writes to a Vite dev-middleware endpoint (`/api/annotations/sync`);
 * boomtime deliberately has NO such endpoint, so `syncToBackend()` early-returns
 * and never issues a network request.
 */
export function isBackendSyncEnabled(): boolean {
  return false;
}
