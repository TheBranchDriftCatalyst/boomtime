import { request as playwrightRequest } from "@playwright/test";
import { BASE_URL, E2E_SPACE_NAME } from "./consts";
import { deleteSpacesByPrefix, ensureE2EUser } from "./helpers";

/**
 * Global teardown: delete any Space named E2E_SPACE_NAME* for the e2e user so
 * reruns start clean. The isolated user + its seeded heartbeats are left in
 * place (cheap, harmless, and speeds up subsequent runs).
 */
export default async function globalTeardown() {
  const request = await playwrightRequest.newContext({ baseURL: BASE_URL });
  try {
    const token = await ensureE2EUser(request);
    await deleteSpacesByPrefix(request, token, E2E_SPACE_NAME);
  } catch {
    // Best-effort cleanup: never fail the run on teardown.
  } finally {
    await request.dispose();
  }
}
