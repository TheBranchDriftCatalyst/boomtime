// Typed fetch client for every backend endpoint. All key/URL/shape tweaks live
// here or in src/types/api.ts so backend key changes are one-line edits.
import { authStore } from "@/features/auth/auth";
import type {
  AuthResponse,
  BadgeLinkPayload,
  CommitReportPayload,
  CreateTokenResponse,
  Credentials,
  CurrentUser,
  DerivedStatus,
  EntityListPayload,
  EntityRedactPayload,
  EntityType,
  AddCurationRuleBody,
  AddCurationRulePayload,
  CrossProjectFile,
  CurationAffectedPayload,
  CurationRule,
  CancelImportPayload,
  HeartbeatAxis,
  HeartbeatFilters,
  HeartbeatGroupPayload,
  HeartbeatListPayload,
  LatestHeartbeatPayload,
  SourceHealth,
  ImportConfigPayload,
  ImportJob,
  ImportLogLine,
  ImportRequest,
  SubmitImportResponse,
  WakatimeRangePayload,
  LeaderboardEntry,
  LeaderboardsPayload,
  AIActivityPayload,
  HealthActivityPayload,
  WorkoutListPayload,
  MomentumPayload,
  ProjectListPayload,
  ProjectStatistics,
  PublicDashboardPayload,
  PunchcardPayload,
  RangeParams,
  RestoreSummary,
  SessionsPayload,
  StatsParams,
  StatsPayload,
  StoredApiToken,
  Space,
  SpaceDetail,
  SpaceRule,
  AddSpaceRuleBody,
  SpaceMatchType,
  SpacePreview,
  TimelinePayload,
  TimelineRange,
  VersionResponse,
  WidgetLinkPayload,
  WidgetLinksPayload,
  WidgetScope,
} from "@/types/api";

export class ApiError extends Error {
  status: number;
  payload: unknown;
  constructor(status: number, message: string, payload: unknown) {
    super(message);
    this.status = status;
    this.payload = payload;
    this.name = "ApiError";
  }
}

type Params = Record<string, string | number | undefined | null>;

// Exported for unit tests. Drops undefined/null/"" params but keeps 0.
export function buildUrl(path: string, params?: Params): string {
  if (!params) return path;
  const usp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== "") usp.append(k, String(v));
  }
  const qs = usp.toString();
  return qs ? `${path}?${qs}` : path;
}

interface RequestOpts {
  method?: string;
  params?: Params;
  body?: unknown;
  auth?: boolean;
}

// Single-flight refresh: concurrent 401s all await the same refresh call.
// Direct fetch (not request()) to avoid recursion when the refresh itself 401s.
let refreshInFlight: Promise<boolean> | null = null;
async function sharedRefresh(): Promise<boolean> {
  if (!refreshInFlight) {
    refreshInFlight = (async () => {
      try {
        const res = await fetch("/auth/refresh_token", {
          method: "POST",
          credentials: "include",
        });
        if (!res.ok) return false;
        const text = await res.text();
        try {
          const data = text ? (JSON.parse(text) as AuthResponse) : null;
          if (data) authStore.update(data);
          return !!data;
        } catch {
          return false;
        }
      } catch {
        // Network error: don't treat as auth failure.
        return false;
      } finally {
        refreshInFlight = null;
      }
    })();
  }
  return refreshInFlight;
}

async function request<T>(path: string, opts: RequestOpts = {}): Promise<T> {
  const { method = "GET", params, body, auth = true } = opts;
  return doRequest<T>(path, { method, params, body, auth }, /* retried */ false);
}

async function doRequest<T>(
  path: string,
  opts: Required<Pick<RequestOpts, "method" | "auth">> & Pick<RequestOpts, "params" | "body">,
  retried: boolean,
): Promise<T> {
  const { method, params, body, auth } = opts;
  const headers: Record<string, string> = {};

  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (auth) {
    const h = authStore.authHeader();
    if (h) headers["Authorization"] = h;
  }

  const res = await fetch(buildUrl(path, params), {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    credentials: "include", // send/receive the HttpOnly refresh_token cookie
  });

  const text = await res.text();
  let data: unknown = undefined;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = text;
    }
  }

  // 401 on an authenticated request: attempt one silent refresh + retry so a
  // just-expired access token doesn't surface as a user-facing failure. The
  // refresh is single-flight so a burst of parallel requests waits on one call.
  if (res.status === 401 && auth && !retried) {
    const ok = await sharedRefresh();
    if (ok) {
      return doRequest<T>(path, opts, /* retried */ true);
    }
  }

  if (!res.ok) {
    const message =
      (data as { message?: string; error?: string })?.message ||
      (data as { error?: string })?.error ||
      res.statusText ||
      "Request failed";
    throw new ApiError(res.status, message, data);
  }

  return data as T;
}

// Several backend GETs wrap their result in a single-key envelope
// ({ rules: [...] }, { jobs: [...] }, { spaces: [...] }, …). `unwrap` fetches
// and returns the bare value (Style A: unwrap at the client boundary) so
// consumers never see the envelope, falling back when the key is absent.
async function unwrap<T>(path: string, key: string, fallback: T): Promise<T> {
  const raw = await request<Record<string, T | undefined>>(path);
  return raw?.[key] ?? fallback;
}

// Envelopes that carry a meaningful second field stay wrapped (they are
// composite payloads, not single-key envelopes), but their types live here.
interface ImportJobDetailPayload {
  job: ImportJob;
  logs: ImportLogLine[];
}

interface CrossProjectFilesPayload {
  files: CrossProjectFile[];
  truncated?: boolean;
}

// --- Auth --------------------------------------------------------------------

export const api = {
  login: (creds: Credentials) =>
    request<AuthResponse>("/auth/login", {
      method: "POST",
      body: creds,
      auth: false,
    }),

  register: (creds: Credentials) =>
    request<AuthResponse>("/auth/register", {
      method: "POST",
      body: creds,
      auth: false,
    }),

  refreshToken: () =>
    request<AuthResponse>("/auth/refresh_token", {
      method: "POST",
      auth: false,
    }),

  logout: () => request<void>("/auth/logout", { method: "POST" }),

  currentUser: () => request<CurrentUser>("/auth/users/current"),

  createApiToken: () =>
    request<CreateTokenResponse>("/auth/create_api_token", { method: "POST" }),

  // Change password (gaka-6jm). Server verifies currentPassword, hashes the
  // new one with argon2id, and revokes every other refresh token for the
  // owner — the caller's access token stays valid so no immediate re-login.
  changePassword: (body: { currentPassword: string; newPassword: string }) =>
    request<void>("/api/v1/users/current/password", {
      method: "POST",
      body,
    }),

  // Encrypted-at-rest imported Wakatime API key (gaka-6jm.2).
  //
  // - GET returns {hasSavedKey, keyStatus?, checkedAt?}. No hint or prefix
  //   of the plaintext is ever surfaced. keyStatus is "valid" | "invalid" |
  //   "unknown" | undefined; checkedAt is RFC3339 or undefined.
  // - POST validates the key against wakatime.com first, then persists
  //   (encrypts under AES-256-GCM). 204 on success; 400 if wakatime.com
  //   rejects the key (surface message: "Wakatime rejected this key…").
  // - DELETE clears the stored ciphertext + status metadata. Idempotent.
  getWakatimeKey: () =>
    request<{
      hasSavedKey: boolean;
      keyStatus?: "valid" | "invalid" | "unknown" | null;
      checkedAt?: string | null;
    }>("/api/v1/users/current/wakatime_key"),
  saveWakatimeKey: (key: string) =>
    request<void>("/api/v1/users/current/wakatime_key", {
      method: "POST",
      body: { key },
    }),
  deleteWakatimeKey: () =>
    request<void>("/api/v1/users/current/wakatime_key", {
      method: "DELETE",
    }),

  // Public profile (gaka-6jm.1). GET returns the caller's toggle + slug so
  // Settings can render the current state and the Sidebar can conditionally
  // show a "Public profile" nav link. PUT writes { enabled, slug }; the
  // server enforces the slug regex, blocks reserved names, and returns 409
  // on slug conflict — surfaced as an ApiError with status=409 that the
  // form maps to an inline "already taken" message.
  getPublicProfile: () =>
    request<{ enabled: boolean; slug: string | null }>(
      "/api/v1/users/current/profile",
    ),
  savePublicProfile: (body: { enabled: boolean; slug: string }) =>
    request<{ enabled: boolean; slug: string | null }>(
      "/api/v1/users/current/profile",
      { method: "PUT", body },
    ),
  // Public payload — no auth. Used by the /p/:slug dashboard route.
  getPublicDashboard: (slug: string) =>
    request<PublicDashboardPayload>(
      `/api/public/profile/${encodeURIComponent(slug)}`,
      { auth: false },
    ),

  // Backend emits hakatime's raw StoredApiToken (default aeson) keys; normalize
  // to the ergonomic shape components use.
  getTokens: async (): Promise<StoredApiToken[]> => {
    const raw = await request<
      Array<{
        tknId: string;
        tknName: string | null;
        tknDesc: string | null;
        lastUsage: string | null;
      }>
    >("/auth/tokens");
    return raw.map((t) => ({
      id: t.tknId,
      name: t.tknName,
      desc: t.tknDesc,
      lastUsage: t.lastUsage,
    }));
  },

  renameToken: (opts: { tokenId: string; tokenName: string }) =>
    request<void>("/auth/token", { method: "POST", body: opts }),

  deleteToken: (tokenId: string) =>
    request<void>(`/auth/token/${encodeURIComponent(tokenId)}`, {
      method: "DELETE",
    }),

  // --- Stats -----------------------------------------------------------------

  getStats: (params: StatsParams) =>
    request<StatsPayload>("/api/v1/users/current/stats", { params }),

  // Top files across ALL projects (with the # of distinct projects each touches).
  getCrossProjectFiles: (params: StatsParams & { limit?: number }) =>
    request<CrossProjectFilesPayload>("/api/v1/users/current/files", { params }),

  // Backend emits hakatime's raw TimelinePayload: { timelineLangs: { lang:
  // [{ tName, tRangeStart, tRangeEnd }] } }. Normalize to { langs: {...} }.
  getTimeline: async (params: StatsParams): Promise<TimelinePayload> => {
    const raw = await request<{
      timelineLangs: Record<
        string,
        Array<{ tName: string; tRangeStart: string; tRangeEnd: string }>
      >;
    }>("/api/v1/users/current/timeline", { params });
    const langs: Record<string, TimelineRange[]> = {};
    for (const [lang, items] of Object.entries(raw.timelineLangs ?? {})) {
      langs[lang] = items.map((i) => ({
        name: i.tName,
        rangeStart: i.tRangeStart,
        rangeEnd: i.tRangeEnd,
      }));
    }
    return { langs };
  },

  // --- Council "big-bet" analytics -------------------------------------------

  getPunchcard: (params: StatsParams) =>
    request<PunchcardPayload>("/api/v1/users/current/stats/punchcard", {
      params,
    }),

  getSessions: (params: StatsParams) =>
    request<SessionsPayload>("/api/v1/users/current/stats/sessions", {
      params,
    }),

  getMomentum: (params: RangeParams & { top?: number }) =>
    request<MomentumPayload>("/api/v1/users/current/stats/momentum", {
      params,
    }),

  // gaka-1l9: AI-assistance per-day metrics + range summary (input/output
  // tokens, AI vs human line changes, distinct sessions, latest plan).
  getAIActivity: (params: RangeParams) =>
    request<AIActivityPayload>("/api/v1/users/current/stats/ai", { params }),

  // Apple Watch / HealthKit per-day workout + sample aggregates. Powers the
  // Wellness card on Overview and the /wellness route. hasData=false when the
  // range has no health data so the card skips render silently.
  getHealthActivity: (params: RangeParams) =>
    request<HealthActivityPayload>("/api/v1/users/current/stats/health", {
      params,
    }),

  // Per-workout event list + per-label aggregate breakdown. Powers the
  // Wellness page's events + by-label sections.
  getWorkoutList: (params: RangeParams) =>
    request<WorkoutListPayload>("/api/v1/users/current/workouts", { params }),

  // --- Projects --------------------------------------------------------------

  getProject: (project: string, params: StatsParams) =>
    request<ProjectStatistics>(
      `/api/v1/users/current/projects/${encodeURIComponent(project)}`,
      { params },
    ),

  getUserProjects: (params: RangeParams) =>
    request<ProjectListPayload>("/api/v1/projects", { params }),

  // --- Leaderboards ----------------------------------------------------------

  // Backend emits hakatime's raw LeaderboardsPayload: { global, lang }.
  // Normalize the per-language key `lang` -> `languages`.
  getLeaderboards: async (params: RangeParams): Promise<LeaderboardsPayload> => {
    const raw = await request<{
      global: LeaderboardEntry[];
      lang: Record<string, LeaderboardEntry[]>;
    }>("/api/v1/leaderboards", { params });
    return { global: raw.global ?? [], languages: raw.lang ?? {} };
  },

  // --- Badges ----------------------------------------------------------------

  getBadgeLink: (project: string) =>
    request<BadgeLinkPayload>(
      `/badge/link/${encodeURIComponent(project)}`,
    ),

  // --- Embeddable widgets ------------------------------------------------------

  getWidgetLink: (scopeType: WidgetScope, scopeRef = "") =>
    request<WidgetLinkPayload>(
      `/api/v1/users/current/widgets/link?scopeType=${encodeURIComponent(scopeType)}&scopeRef=${encodeURIComponent(scopeRef)}`,
    ),

  listWidgetLinks: () =>
    request<WidgetLinksPayload>("/api/v1/users/current/widgets/links"),

  // Delete was removed: rolling covers the "invalidate a leaked URL" use case
  // without leaving a scope in a link-less state (see internal/db/widgets.go).

  rollWidgetLink: (linkId: string) =>
    request<WidgetLinkPayload>(
      `/api/v1/users/current/widgets/link/${encodeURIComponent(linkId)}/roll`,
      { method: "POST" },
    ),

  // --- Import ----------------------------------------------------------------

  getImportConfig: () => request<ImportConfigPayload>("/import/config"),

  // Ask wakatime.com how far back the user's data goes, to pre-fill the range.
  // Pass the typed token (base64) when present; otherwise {} uses the env key.
  detectWakatimeRange: (body: { apiToken?: string } = {}) =>
    request<WakatimeRangePayload>("/import/wakatime-range", {
      method: "POST",
      body,
    }),

  // Start an import; returns the durable job id to bind to over WebSocket.
  submitImport: (body: ImportRequest) =>
    request<SubmitImportResponse>("/import", { method: "POST", body }),

  // First-class import jobs. The backend wraps the list in { jobs: [...] };
  // unwrap to a bare ImportJob[].
  getImportJobs: () => unwrap<ImportJob[]>("/import/jobs", "jobs", []),

  getImportJob: (id: number) =>
    request<ImportJobDetailPayload>(`/import/jobs/${id}`),

  cancelImportJob: (id: number) =>
    request<CancelImportPayload>(`/import/jobs/${id}/cancel`, {
      method: "POST",
    }),

  // --- Derived-data health (gap_seconds + rollup) ----------------------------

  // --- Source health (ingestion / "is my plugin still reporting" view) -------

  // Backend wraps the list in { sources: [...] }; unwrap to a bare
  // SourceHealth[].
  getSourceHealth: () =>
    unwrap<SourceHealth[]>("/api/v1/users/current/sources/health", "sources", []),

  getDerivedStatus: () =>
    request<DerivedStatus>("/api/v1/users/current/derived/status"),

  resyncDerived: () =>
    request<DerivedStatus>("/api/v1/users/current/derived/resync", {
      method: "POST",
    }),

  // --- Whole-database backup (Save DB / Load DB) -----------------------------

  // Raw fetch (not request()): the response body is a zip Blob, not JSON.
  exportDb: async (): Promise<Blob> => {
    const headers: Record<string, string> = {};
    const h = authStore.authHeader();
    if (h) headers["Authorization"] = h;
    const res = await fetch("/api/v1/users/current/db/export", {
      headers,
      credentials: "include",
    });
    if (!res.ok) {
      throw new ApiError(res.status, res.statusText || "Export failed", undefined);
    }
    return res.blob();
  },

  // Uploads the backup archive as the raw request body and REPLACES the entire
  // database with it. The confirm param is the server-side accident guard; the
  // typed-REPLACE modal is the human one.
  importDb: async (file: File): Promise<RestoreSummary> => {
    const headers: Record<string, string> = { "Content-Type": "application/zip" };
    const h = authStore.authHeader();
    if (h) headers["Authorization"] = h;
    const res = await fetch(
      buildUrl("/api/v1/users/current/db/import", { confirm: "replace-all-data" }),
      { method: "POST", headers, body: file, credentials: "include" },
    );
    const text = await res.text();
    let data: unknown;
    try {
      data = text ? JSON.parse(text) : undefined;
    } catch {
      data = text;
    }
    if (!res.ok) {
      const message =
        (data as { message?: string; error?: string })?.message ||
        (data as { error?: string })?.error ||
        res.statusText ||
        "Restore failed";
      throw new ApiError(res.status, message, data);
    }
    return data as RestoreSummary;
  },

  // --- Commits ---------------------------------------------------------------

  getCommitLog: (
    project: string,
    params: { repoOwner: string; repoName: string; user: string; limit?: number },
  ) =>
    request<CommitReportPayload>(
      `/api/v1/commits/${encodeURIComponent(project)}/report`,
      { params },
    ),

  // --- Heartbeats explorer ---------------------------------------------------

  // Group heartbeats by a single axis, filtered by the accumulated drill path.
  // Each accumulated filter is sent as its own query param. entity is an
  // ILIKE substring narrower applied server-side to BOTH the group listing
  // AND the leaf rows (mirrors listHeartbeats), so the Explorer search box
  // narrows the visible tree, not just leaves.
  groupHeartbeats: (opts: {
    groupBy: HeartbeatAxis;
    start: string;
    end: string;
    timeLimit?: number;
    filters?: HeartbeatFilters;
    entity?: string;
  }) =>
    request<HeartbeatGroupPayload>("/api/v1/users/current/heartbeats/group", {
      params: {
        groupBy: opts.groupBy,
        start: opts.start,
        end: opts.end,
        timeLimit: opts.timeLimit,
        entity: opts.entity,
        ...(opts.filters ?? {}),
      },
    }),

  // Most-recent heartbeat marker, for the import "backfill from last" button.
  getLatestHeartbeat: () =>
    request<LatestHeartbeatPayload>(
      "/api/v1/users/current/heartbeats/latest",
    ),

  // --- Entity Explorer (gaka-90x) --------------------------------------------

  // Per-ty flat list of every entity the owner has, with count + first/last seen.
  listEntitiesByType: (ty: EntityType, limit = 500) =>
    request<EntityListPayload>(
      "/api/v1/users/current/heartbeats/entities",
      { params: { type: ty, limit } },
    ),

  // Blank the entity column on every heartbeat matching (ty, entity ∈
  // entities). Heartbeat rows stay — only the entity value is scrubbed, so
  // per-project/language/machine totals are unchanged. Owner-scoped
  // server-side. The ?confirm= sentinel is the accident guard.
  redactEntities: (ty: EntityType, entities: string[]) =>
    request<EntityRedactPayload>(
      buildUrl("/api/v1/users/current/heartbeats/entities/redact", {
        confirm: "redact-entities",
      }),
      { method: "POST", body: { ty, entities } },
    ),

  // Paginated raw heartbeat rows for a fully-drilled leaf.
  listHeartbeats: (opts: {
    start: string;
    end: string;
    filters?: HeartbeatFilters;
    entity?: string;
    page?: number;
    limit?: number;
  }) =>
    request<HeartbeatListPayload>("/api/v1/users/current/heartbeats", {
      params: {
        start: opts.start,
        end: opts.end,
        entity: opts.entity || undefined,
        page: opts.page,
        limit: opts.limit,
        ...(opts.filters ?? {}),
      },
    }),

  // --- Data curation ---------------------------------------------------------

  // Backend wraps the list in { rules: [...] }; unwrap to a bare CurationRule[].
  getCurationRules: () =>
    unwrap<CurationRule[]>("/api/v1/users/current/curation", "rules", []),

  addCurationRule: (body: AddCurationRuleBody) =>
    request<AddCurationRulePayload>("/api/v1/users/current/curation", {
      method: "POST",
      body,
    }),

  deleteCurationRule: (id: number) =>
    request<void>(`/api/v1/users/current/curation/${id}`, {
      method: "DELETE",
    }),

  // Raw values a rule currently matches (for previewing a regex remapping).
  getCurationRuleAffected: (id: number) =>
    request<CurationAffectedPayload>(
      `/api/v1/users/current/curation/${id}/affected`,
    ),

  // --- Spaces (named, rule-based scopes) -------------------------------------

  // Backend wraps the list in { spaces: [...] } (curation convention); unwrap
  // so the public shape stays a bare Space[].
  getSpaces: () => unwrap<Space[]>("/api/v1/users/current/spaces", "spaces", []),

  getSpace: (id: number | string) =>
    request<SpaceDetail>(`/api/v1/users/current/spaces/${id}`),

  // Backend wraps the created space in { space: {...} }; unwrap to a bare Space.
  createSpace: async (name: string): Promise<Space> => {
    const raw = await request<{ space: Space }>(
      "/api/v1/users/current/spaces",
      { method: "POST", body: { name } },
    );
    return raw.space;
  },

  // PATCH returns 204 No Content.
  renameSpace: (
    id: number | string,
    body: { name?: string; position?: number },
  ) =>
    request<void>(`/api/v1/users/current/spaces/${id}`, {
      method: "PATCH",
      body,
    }),

  deleteSpace: (id: number | string) =>
    request<void>(`/api/v1/users/current/spaces/${id}`, { method: "DELETE" }),

  // Backend wraps the created rule in { rule: {...} }; unwrap to a bare SpaceRule.
  addSpaceRule: async (
    id: number | string,
    body: AddSpaceRuleBody,
  ): Promise<SpaceRule> => {
    const raw = await request<{ rule: SpaceRule }>(
      `/api/v1/users/current/spaces/${id}/rules`,
      { method: "POST", body },
    );
    return raw.rule;
  },

  deleteSpaceRule: (id: number | string, rid: number | string) =>
    request<void>(`/api/v1/users/current/spaces/${id}/rules/${rid}`, {
      method: "DELETE",
    }),

  // Live preview of the raw values an unsaved rule would match.
  getSpacePreview: (params: {
    axis: string;
    matchValue: string;
    matchType: SpaceMatchType;
  }) =>
    request<SpacePreview>("/api/v1/users/current/spaces/preview", { params }),

  // --- Meta (version + changelog) -------------------------------------------

  // Running app version — the git-describe string stamped by ldflags. Falls
  // back to "dev" for a bare `go build` in an untagged tree.
  getVersion: () =>
    request<VersionResponse>("/api/v1/version", { auth: false }),

  // Raw CHANGELOG.md as text (request() falls through to raw text when the
  // response isn't JSON, so this "just works").
  getChangelog: () =>
    request<string>("/api/v1/changelog", { auth: false }),
};
