import { mkdirSync } from "node:fs";
import path from "node:path";
import { request as playwrightRequest } from "@playwright/test";
import type { FullConfig } from "@playwright/test";
import {
  BACKEND_URL,
  BASE_URL,
  E2E_SPACE_NAME,
  STORAGE_STATE,
} from "./consts";
import {
  createSpace,
  deleteSpacesByPrefix,
  ensureE2EUser,
  seedHeartbeats,
  waitForUrl,
} from "./helpers";

/**
 * Global setup: readiness-poll the stack, register/login an isolated e2e user,
 * bulk-seed heartbeats (fixture + deterministic target), guarantee a fresh
 * Space, and capture the refresh_token cookie into storageState so the browser
 * boots authenticated.
 */
export default async function globalSetup(_config: FullConfig) {
  // A request context scoped to the SAME origin the browser tests use, so the
  // refresh_token Set-Cookie is stored against http://localhost:5173.
  const request = await playwrightRequest.newContext({ baseURL: BASE_URL });

  try {
    // 1. Fail fast with a clear message if the stack is down.
    await waitForUrl(request, `${BASE_URL}/`, "Vite dev server (:5173)");
    await waitForUrl(request, `${BACKEND_URL}/`, "Go backend (:8080)");

    // 2. Isolated e2e user (idempotent). Sets the refresh_token cookie on this
    // request context.
    const token = await ensureE2EUser(request);

    // 3. Seed heartbeats for THIS user only.
    await seedHeartbeats(request, token);

    // 4. Fresh Space: delete any pre-existing E2E_SPACE_NAME* for a clean slate,
    // then create exactly one.
    await deleteSpacesByPrefix(request, token, E2E_SPACE_NAME);
    await createSpace(request, token, E2E_SPACE_NAME);

    // 5. Persist the authenticated browser state (refresh_token cookie). The SPA
    // bootstraps its in-memory access token from this cookie via
    // POST /auth/refresh_token on load.
    mkdirSync(path.dirname(STORAGE_STATE), { recursive: true });
    await request.storageState({ path: STORAGE_STATE });
  } finally {
    await request.dispose();
  }
}
