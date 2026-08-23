import { spawnSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";
import type { APIRequestContext } from "@playwright/test";
import {
  BACKEND_URL,
  BASE_URL,
  E2E_PASSWORD,
  E2E_TARGET_PROJECT,
  E2E_USERNAME,
} from "./consts";

// The committed backend fixture (3000 rows, camelCase, dated ~Apr–Jul 2026).
const FIXTURE_PATH = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  // Moved with the internal/{shared,books,boomtime} domain split — the old
  // internal/db path made globalSetup throw ENOENT, which aborted the ENTIRE
  // e2e suite before a single spec ran.
  "../../internal/shared/db/testdata/heartbeats_fixture.json",
);

// Keep ingest snappy: the spec only needs realistic data inside the default
// (recent) window, not the whole 3000-row corpus.
const FIXTURE_CAP = 400;

interface FixtureHeartbeat {
  project: string | null;
  language: string | null;
  editor: string | null;
  plugin: string | null;
  platform: string | null;
  machine: string | null;
  branch: string | null;
  category: string | null;
  entity: string | null;
  type: string | null;
  isWrite: boolean | null;
  lineno: number | null;
  cursorpos: string | null;
  fileLines: number | null;
  dependencies: string[] | null;
  userAgent: string | null;
  timeSent: string;
}

// Wire shape the bulk endpoint expects (snake_case, `time` in epoch seconds).
interface WireHeartbeat {
  project: string | null;
  language: string | null;
  editor: string | null;
  plugin: string | null;
  platform: string | null;
  machine: string | null;
  branch: string | null;
  category: string | null;
  entity: string | null;
  type: string | null;
  is_write: boolean | null;
  lineno: number | null;
  lines: number | null;
  dependencies: string[] | null;
  user_agent: string | null;
  time: number;
}

/** Register (idempotent: fall back to login) and return the access token. */
export async function ensureE2EUser(
  request: APIRequestContext,
): Promise<string> {
  const creds = { username: E2E_USERNAME, password: E2E_PASSWORD };
  const reg = await request.post(`${BASE_URL}/auth/register`, { data: creds });
  if (reg.ok()) {
    return (await reg.json()).token as string;
  }
  // Already exists (or other 4xx): log in with the same creds. This also sets
  // the refresh_token cookie on the request context.
  const login = await request.post(`${BASE_URL}/auth/login`, { data: creds });
  if (!login.ok()) {
    throw new Error(
      `e2e user auth failed: register ${reg.status()}, login ${login.status()}`,
    );
  }
  return (await login.json()).token as string;
}

/**
 * Load the committed fixture, map camelCase→wire snake_case, and shift every
 * timestamp so the LATEST fixture day lands ~now — this places the realistic
 * data inside the explorer's default (recent, 15-day) window.
 */
function buildFixtureHeartbeats(): WireHeartbeat[] {
  const parsed = JSON.parse(readFileSync(FIXTURE_PATH, "utf8")) as {
    heartbeats: FixtureHeartbeat[];
  };
  const rows = parsed.heartbeats.slice(0, FIXTURE_CAP);
  const now = Date.now();
  const maxSent = Math.max(...rows.map((h) => Date.parse(h.timeSent)));
  const delta = now - maxSent; // shift so max(timeSent) → now

  return rows.map((h) => ({
    project: h.project,
    language: h.language,
    editor: h.editor,
    plugin: h.plugin,
    platform: h.platform,
    machine: h.machine,
    branch: h.branch,
    category: h.category,
    entity: h.entity,
    type: h.type,
    is_write: h.isWrite,
    lineno: h.lineno,
    lines: h.fileLines, // fileLines → lines
    dependencies: h.dependencies,
    user_agent: h.userAgent, // userAgent → user_agent
    // cursorpos is a string in the fixture vs int in the wire; drop it.
    time: (Date.parse(h.timeSent) + delta) / 1000, // epoch SECONDS (float)
  }));
}

/**
 * A small deterministic TARGET: ~5 heartbeats for E2E_TARGET_PROJECT within the
 * last hour, so the spec always has a stable top-level project row regardless
 * of the anonymized fixture names.
 */
function buildTargetHeartbeats(): WireHeartbeat[] {
  const now = Date.now() / 1000;
  return Array.from({ length: 5 }, (_, i) => ({
    project: E2E_TARGET_PROJECT,
    language: "TypeScript",
    editor: "VS Code",
    plugin: "vscode-wakatime",
    platform: "Mac",
    machine: "e2e",
    branch: "main",
    category: "Coding",
    entity: `src/e2e/file${i}.ts`,
    type: "file",
    is_write: true,
    lineno: 1,
    lines: 100 + i,
    dependencies: [],
    user_agent: "wakatime/1.0",
    time: now - i * 60, // spaced a minute apart, all within the last hour
  }));
}

/** Bulk-seed the e2e user's heartbeats (fixture + deterministic target). */
export async function seedHeartbeats(
  request: APIRequestContext,
  token: string,
): Promise<void> {
  const body = [...buildFixtureHeartbeats(), ...buildTargetHeartbeats()];
  const res = await request.post(
    `${BASE_URL}/api/v1/users/current/heartbeats.bulk`,
    {
      headers: {
        Authorization: `Basic ${token}`,
        "Content-Type": "application/json",
        "X-Machine-Name": "e2e",
      },
      data: body,
    },
  );
  if (res.status() !== 202 && !res.ok()) {
    throw new Error(
      `seed heartbeats failed: ${res.status()} ${await res.text()}`,
    );
  }
}

/**
 * Seed a rich, deterministic reading library (reading_items + reading_activity)
 * for the e2e user so the Reading dashboard + Books Explore specs have data.
 *
 * Reading data ONLY arrives via the Amazon/Audible sync path — there is no HTTP
 * ingest for it — so unlike heartbeats we cannot seed it over the API. Instead
 * we invoke the dev-gated `boomtime seed-reading-demo` subcommand operator-side,
 * connecting straight to the same Postgres the stack uses (config.Load() reads
 * the repo .env, BOOM_ENV=dev).
 *
 * Best-effort by design: if the Go toolchain / binary isn't reachable this logs
 * and returns rather than hard-failing global-setup — the reading specs are
 * written to still render their empty-state tiles, so a missing seed degrades a
 * data-rich assertion to an empty-state one instead of aborting the whole run.
 *
 * Invocation, in order of preference (first that exists wins):
 *   1. bin/boomtime seed-reading-demo --user <e2e user>   (a prebuilt binary)
 *   2. go run ./cmd/boomtime seed-reading-demo --user <e2e user>
 * Both run with cwd = repo root so godotenv picks up ./.env.
 */
export function seedReadingDemo(username: string = E2E_USERNAME): void {
  const repoRoot = path.resolve(
    path.dirname(fileURLToPath(import.meta.url)),
    "../..",
  );
  const args = ["seed-reading-demo", "--user", username];

  const prebuilt = path.join(repoRoot, "bin", "boomtime");
  const invocation: { cmd: string; argv: string[] } = existsSync(prebuilt)
    ? { cmd: prebuilt, argv: args }
    : { cmd: "go", argv: ["run", "./cmd/boomtime", ...args] };

  const res = spawnSync(invocation.cmd, invocation.argv, {
    cwd: repoRoot,
    // Default BOOM_ENV=dev so the subcommand's prod-safety gate lets the seed
    // through even if the env didn't already set it (.env also sets it).
    env: { ...process.env, BOOM_ENV: process.env.BOOM_ENV ?? "dev" },
    encoding: "utf8",
  });

  if (res.error) {
    console.warn(
      `[e2e] seed-reading-demo skipped (could not launch ${invocation.cmd}): ${res.error.message}`,
    );
    return;
  }
  if (res.status !== 0) {
    console.warn(
      `[e2e] seed-reading-demo exited ${res.status}: ${res.stderr?.trim() || res.stdout?.trim()}`,
    );
    return;
  }
  console.log(`[e2e] ${res.stdout?.trim() || "seeded reading demo"}`);
}

interface Space {
  id: number;
  name: string;
}

/** List the e2e user's Spaces. */
export async function listSpaces(
  request: APIRequestContext,
  token: string,
): Promise<Space[]> {
  const res = await request.get(`${BASE_URL}/api/v1/users/current/spaces`, {
    headers: { Authorization: `Basic ${token}` },
  });
  if (!res.ok()) {
    throw new Error(`list spaces failed: ${res.status()}`);
  }
  return ((await res.json()).spaces ?? []) as Space[];
}

/** Delete every Space whose name starts with `prefix` for the e2e user. */
export async function deleteSpacesByPrefix(
  request: APIRequestContext,
  token: string,
  prefix: string,
): Promise<void> {
  const spaces = await listSpaces(request, token);
  for (const s of spaces) {
    if (s.name.startsWith(prefix)) {
      await request.delete(
        `${BASE_URL}/api/v1/users/current/spaces/${s.id}`,
        { headers: { Authorization: `Basic ${token}` } },
      );
    }
  }
}

/** Create a Space and return it. */
export async function createSpace(
  request: APIRequestContext,
  token: string,
  name: string,
): Promise<Space> {
  const res = await request.post(`${BASE_URL}/api/v1/users/current/spaces`, {
    headers: {
      Authorization: `Basic ${token}`,
      "Content-Type": "application/json",
    },
    data: { name },
  });
  if (!res.ok()) {
    throw new Error(`create space failed: ${res.status()} ${await res.text()}`);
  }
  return (await res.json()).space as Space;
}

/** Add a membership rule to a Space and return nothing (throws on failure). */
export async function addSpaceRule(
  request: APIRequestContext,
  token: string,
  spaceId: number,
  body: { axis: string; matchValue: string; matchType: "exact" | "regex" },
): Promise<void> {
  const res = await request.post(
    `${BASE_URL}/api/v1/users/current/spaces/${spaceId}/rules`,
    {
      headers: {
        Authorization: `Basic ${token}`,
        "Content-Type": "application/json",
      },
      data: body,
    },
  );
  if (!res.ok()) {
    throw new Error(
      `add space rule failed: ${res.status()} ${await res.text()}`,
    );
  }
}

/** Exchange the storageState refresh cookie for a fresh access token. */
export async function refreshAccessToken(
  request: APIRequestContext,
): Promise<string> {
  const res = await request.post(`${BASE_URL}/auth/refresh_token`);
  if (!res.ok()) {
    throw new Error(`refresh_token failed: ${res.status()}`);
  }
  return (await res.json()).token as string;
}

/** Poll a URL until it answers (or throw with a clear message on timeout). */
export async function waitForUrl(
  request: APIRequestContext,
  url: string,
  label: string,
  timeoutMs = 30_000,
): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let lastErr = "";
  while (Date.now() < deadline) {
    try {
      const res = await request.get(url, { timeout: 5_000 });
      if (res.status() > 0) return; // any HTTP answer means it's up
    } catch (e) {
      lastErr = String(e);
    }
    await new Promise((r) => setTimeout(r, 1_000));
  }
  throw new Error(
    `Dev stack not reachable: ${label} at ${url} did not answer within ` +
      `${timeoutMs}ms. Is the docker stack up? (docker compose up -d). ` +
      `Last error: ${lastErr}`,
  );
}

export { BACKEND_URL, BASE_URL };

// -------------------------------------------------------------------------
// boom-dvb — helpers for the recent-features Playwright suite (admin
// sidebar, swagger docs, backfill tab, avatar tab, public dossier).
//
// These are additive: the existing add-to-space/widgets specs continue to
// boot preauthenticated via storageState. The specs added under this bead
// need distinct login shapes (admin vs non-admin) that the shared
// storageState can't provide, so we log in per-test into a fresh browser
// context and clear the storageState cookie first.
// -------------------------------------------------------------------------

import type { BrowserContext, Page } from "@playwright/test";

/**
 * BOOMTIME_E2E_ADMIN_USER + BOOMTIME_E2E_ADMIN_PASS credentials for a user
 * that is on BOOM_ADMIN_USERS. If unset, admin-scoped specs will skip.
 * The dev stack default admin is `panda` (see .env), so setting
 * BOOMTIME_E2E_ADMIN_USER=panda + a matching password will opt in.
 */
export const ADMIN_USERNAME = process.env.BOOMTIME_E2E_ADMIN_USER ?? "";
export const ADMIN_PASSWORD = process.env.BOOMTIME_E2E_ADMIN_PASS ?? "";

/**
 * Non-admin credentials. Defaults to the isolated e2e user that
 * global-setup already creates; that user is NOT on BOOM_ADMIN_USERS in
 * the shipped dev stack. Override via env vars if you're pointing at a
 * remote boomtime and don't want to register the e2e-playwright-user
 * there.
 */
export const NONADMIN_USERNAME =
  process.env.BOOMTIME_E2E_NONADMIN_USER ?? E2E_USERNAME;
export const NONADMIN_PASSWORD =
  process.env.BOOMTIME_E2E_NONADMIN_PASS ?? E2E_PASSWORD;

/** True when the browser tests can hit a running boomtime dev/prod stack. */
export function stackReachableFromEnv(): boolean {
  // Vite dev server on :5173 is the default target from playwright.config.ts;
  // BOOMTIME_BASE_URL / PLAYWRIGHT_BASE_URL let CI point at a remote host.
  // We can't fetch synchronously here, so the actual liveness check happens
  // in globalSetup — if it failed the whole suite would already have aborted.
  // This predicate is used by the AVATAR + BACKFILL + SWAGGER specs to
  // skip when explicitly disabled via BOOMTIME_E2E_SKIP=1.
  return process.env.BOOMTIME_E2E_SKIP !== "1";
}

/**
 * Log in via /auth/login inside the browser context. Clears any
 * pre-existing storageState cookies first so the storageState admin/non-
 * admin identity doesn't collide with the one we want.
 *
 * The SPA bootstraps its in-memory access token from POST
 * /auth/refresh_token on load; setting the refresh_token cookie via the
 * login call satisfies that bootstrap and every subsequent authed request
 * on the page reuses it.
 */
export async function loginAsUser(
  page: Page,
  username: string,
  password: string,
): Promise<void> {
  // Drop the storageState cookies for this origin — we don't want the
  // e2e-playwright-user refresh cookie leaking into an admin session.
  await page.context().clearCookies();
  // Also clear any localStorage from previous tests (we want the app's
  // welcome-modal-suppressed flag though, so re-seed it).
  await page.goto("/login");
  await page.evaluate(() => {
    localStorage.clear();
    localStorage.setItem("boomtime-welcomed", "1");
    localStorage.setItem("theme:name", JSON.stringify("boomtime"));
    localStorage.setItem("theme:variant", JSON.stringify("dark"));
  });
  const res = await page.request.post("/auth/login", {
    data: { username, password },
  });
  if (!res.ok()) {
    throw new Error(
      `login failed for ${username}: ${res.status()} ${await res.text()}`,
    );
  }
}

/**
 * Convenience: log in as the admin fixture. Returns false (so the caller
 * can `test.skip`) when no admin credentials are configured — the
 * boom-ebq / boom-vh8 / boom-9v4 specs treat missing admin creds as
 * "environment not wired for this test", not a failure.
 */
export async function loginAsAdmin(page: Page): Promise<boolean> {
  if (!ADMIN_USERNAME || !ADMIN_PASSWORD) return false;
  await loginAsUser(page, ADMIN_USERNAME, ADMIN_PASSWORD);
  return true;
}

/** Convenience: log in as a known non-admin. Uses the e2e-playwright-user
 * global-setup already registered. */
export async function loginAsNonAdmin(page: Page): Promise<void> {
  await loginAsUser(page, NONADMIN_USERNAME, NONADMIN_PASSWORD);
}

/**
 * Try to fetch the public-profile slug for the calling test's fixture
 * user. Returns null when the user has no public profile (which is the
 * expected state for a freshly-registered non-admin) so the caller can
 * `test.skip`.
 */
export async function tryGetPublicProfileSlug(
  context: BrowserContext,
  username: string,
): Promise<string | null> {
  const res = await context.request.get(
    `/api/public/profile/${encodeURIComponent(username)}`,
  );
  if (res.status() === 404) return null;
  if (!res.ok()) return null;
  return username;
}

/**
 * Best-effort revoke of any API tokens whose name matches the given
 * substring. Called in afterEach to clean up tokens minted during swagger
 * FAB tests. Never throws — token cleanup MUST NOT fail a passing test.
 */
export async function revokeTokensByNameSubstring(
  page: Page,
  substring: string,
): Promise<void> {
  try {
    const list = await page.request.get("/auth/tokens");
    if (!list.ok()) return;
    const rows = (await list.json()) as Array<{ id: string; name?: string }>;
    for (const t of rows) {
      if ((t.name ?? "").includes(substring)) {
        await page.request.delete(
          `/auth/token/${encodeURIComponent(t.id)}`,
        );
      }
    }
  } catch {
    /* swallow — cleanup is best-effort */
  }
}

/**
 * Standard skip reason for the recent-features specs. Specs call
 * `test.skip(!stackReachableFromEnv(), NO_STACK_REASON)` to opt out
 * when BOOMTIME_E2E_SKIP=1 is set.
 */
export const NO_STACK_REASON =
  "BOOMTIME_E2E_SKIP=1 — recent-features specs opted out (no live stack)";

export const NO_ADMIN_CREDS_REASON =
  "BOOMTIME_E2E_ADMIN_USER / BOOMTIME_E2E_ADMIN_PASS not set — admin-only spec cannot run";

