// Shared constants for the add-to-Space e2e suite. Used by global-setup,
// global-teardown, and the spec so the seeded target project + Space name stay
// in sync across all three.

/** Base URL the browser + request contexts hit (Vite dev server, proxies API). */
export const BASE_URL = "http://localhost:5173";

/** Go backend, polled for readiness in global-setup. */
export const BACKEND_URL = "http://localhost:8080";

/**
 * Dedicated, isolated e2e user. NEVER touch panda's real data — everything is
 * seeded under this account. Setup is idempotent (register, else login).
 */
export const E2E_USERNAME = "e2e-playwright-user";
export const E2E_PASSWORD = "e2e-playwright-pass-123";

/**
 * A stable, deterministic top-level project row the spec targets. Seeded on top
 * of the anonymized fixture so the test never depends on fixture project names.
 */
export const E2E_TARGET_PROJECT = "e2e-space-target";

/** The Space the spec adds the target project to. Teardown deletes any Space
 * whose name starts with this string for the e2e user. */
export const E2E_SPACE_NAME = "E2E Playwright Space";

/** Where Playwright persists the authenticated browser storageState. */
export const STORAGE_STATE = "e2e/.auth/state.json";
