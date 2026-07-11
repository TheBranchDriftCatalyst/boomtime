import { readFileSync } from "node:fs";
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
  "../../internal/db/testdata/heartbeats_fixture.json",
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
