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
  ApplyRenamePayload,
  CurationActionPreviewPayload,
  PurgeHiddenPayload,
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
  LocPayload,
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
  PublicConfig,
  AdminUsersPayload,
  IdentitiesPayload,
  GithubConnection,
  GithubStatsPayload,
  WidgetLinkPayload,
  WidgetLinksPayload,
  WidgetScope,
  // gaka-wpb: goals feature types.
  BatchGoalProgress,
  CreateGoalBody,
  Goal,
  GoalProgress,
  UpdateGoalBody,
} from "@/types/api";
import type {
  LabelCatalogRow,
  LabelsCatalogPayload,
} from "@/features/publicprofile/labels/types";

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

// Top-N debug rollup entry surfaced by /api/v1/admin/backfill/stats to power
// the Synthetic heartbeat inspector on the Backfill admin tab.
export interface BackfillRollupEntry {
  name: string;
  seconds: number;
  rows: number;
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

  // gaka-93f.1.1: public client-config advertisement. Unauthenticated; read
  // once at boot so the FE can branch the signup CTA (local vs Authentik),
  // show/hide billing, and honor the beta-onboarding kill switch.
  publicConfig: () =>
    request<PublicConfig>("/api/v1/config/public", { auth: false }),

  refreshToken: () =>
    request<AuthResponse>("/auth/refresh_token", {
      method: "POST",
      auth: false,
    }),

  logout: () => request<void>("/auth/logout", { method: "POST" }),

  currentUser: () => request<CurrentUser>("/auth/users/current"),

  createApiToken: (body?: { name?: string }) =>
    request<CreateTokenResponse>("/auth/create_api_token", {
      method: "POST",
      body: body?.name ? { name: body.name } : undefined,
    }),

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

  // Per-user IANA timezone (gaka-dg7).
  //
  // GET returns {timezone, effectiveTimezone}:
  //   - `timezone` is the raw stored value (empty = user has never picked).
  //   - `effectiveTimezone` is what the server ACTUALLY uses via the
  //     3-level resolver (user > BOOM_DEFAULT_TIMEZONE > "UTC"). NEVER "".
  //
  // PATCH validates against Go's time.LoadLocation. An empty timezone
  // clears the explicit pick (falls back to server default in the resolver).
  // Server rebuilds hb_rollup_daily on success so the Overview fast path
  // serves user-local buckets immediately.
  getTimezone: () =>
    request<{ timezone: string; effectiveTimezone: string }>(
      "/api/v1/users/current/timezone",
    ),
  updateTimezone: (timezone: string) =>
    request<{ timezone: string; effectiveTimezone: string }>(
      "/api/v1/users/current/timezone",
      { method: "PATCH", body: { timezone } },
    ),

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
  // gaka-174.7: optional `days` re-scopes the STATS window (server clamps to
  // 1..365, default 60). Labels/awards come from a separate endpoint that
  // stays on the canonical window, so re-scoping never touches them.
  getPublicDashboard: (slug: string, days?: number) =>
    request<PublicDashboardPayload>(
      `/api/public/profile/${encodeURIComponent(slug)}` +
        (days ? `?days=${days}` : ""),
      { auth: false },
    ),

  // Dashboard layout persistence (gaka-keb). Per-user, per-scope.
  // GET returns `{ layout: ... }` or 404 when no layout is saved. PUT
  // upserts. DELETE clears (FE reverts to default). Small (4 KiB) body cap
  // enforced server-side; the FE typically emits well under 2 KiB.
  getDashboardLayout: (scope: string) =>
    request<{ layout: unknown }>(
      `/api/v1/users/current/dashboard/${encodeURIComponent(scope)}`,
    ),
  putDashboardLayout: (scope: string, layout: unknown) =>
    request<{ layout: unknown }>(
      `/api/v1/users/current/dashboard/${encodeURIComponent(scope)}`,
      { method: "PUT", body: { layout } },
    ),
  deleteDashboardLayout: (scope: string) =>
    request<void>(
      `/api/v1/users/current/dashboard/${encodeURIComponent(scope)}`,
      { method: "DELETE" },
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

  // gaka-yfg: total + per-project lines of code (current snapshot) plus a
  // bounded total-LOC-over-time growth curve, derived from file_lines with the
  // generated/vendored ignore filter applied server-side. No GitHub dependency.
  getLoc: (params: StatsParams) =>
    request<LocPayload>("/api/v1/users/current/stats/loc", { params }),

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

  // Both the create and edit paths (edit calls this after an optional delete)
  // go through here, so normalize `applyAtIngest` to an explicit boolean once —
  // absent means false ("query-time view rule only"), matching the backend
  // default.
  addCurationRule: (body: AddCurationRuleBody) =>
    request<AddCurationRulePayload>("/api/v1/users/current/curation", {
      method: "POST",
      body: { ...body, applyAtIngest: body.applyAtIngest ?? false },
    }),

  deleteCurationRule: (id: number) =>
    request<void>(`/api/v1/users/current/curation/${id}`, {
      method: "DELETE",
    }),

  // gaka-dfd: pause / resume a curation rule without deleting it. Body is
  // optional — omit to flip the current value, or pass `enabled` explicitly
  // to set an exact state (defends against double-click races). Returns the
  // new enabled value.
  toggleCurationRule: (id: number, enabled?: boolean) =>
    request<{ enabled: boolean }>(
      `/api/v1/users/current/curation/${id}/toggle`,
      {
        method: "POST",
        body: enabled === undefined ? undefined : { enabled },
      },
    ),

  // Raw values a rule currently matches (for previewing a regex remapping).
  getCurationRuleAffected: (id: number) =>
    request<CurationAffectedPayload>(
      `/api/v1/users/current/curation/${id}/affected`,
    ),

  // gaka-cr4 + gaka-due: preview a destructive curation action. The response
  // shape is a discriminated union on rule.action — rename rules get the
  // apply-preview payload (UPDATE + rule-delete SQL, before/after diff),
  // hide rules get the purge-preview payload (DELETE heartbeats + rule-delete
  // SQL, per-row "will be deleted" info). ONE endpoint serves both variants;
  // the FE modal reads the `action` field to render the right UI. No data
  // is mutated.
  previewCurationAction: (id: number) =>
    request<CurationActionPreviewPayload>(
      `/api/v1/users/current/curation/${id}/preview`,
    ),

  // gaka-cr4: DESTRUCTIVELY apply a rename rule — rewrites raw heartbeat rows
  // (UPDATE) and removes the rule row itself, atomically. Rejects non-rename
  // rules with 400. Returned sqlRun matches the preview's sqlPlanned verbatim.
  applyCurationRule: (id: number) =>
    request<ApplyRenamePayload>(
      `/api/v1/users/current/curation/${id}/apply`,
      { method: "POST" },
    ),

  // gaka-due: DESTRUCTIVELY purge a hide rule — DELETEs every heartbeat row
  // the rule matches, then removes the rule row itself, atomically. Rejects
  // non-hide rules with 400. Data-obliterating: the FE gates this behind a
  // "type rule id N to confirm" input to prevent muscle-memory Enter
  // presses. Returned sqlRun matches the preview's sqlPlanned verbatim.
  purgeCurationRule: (id: number) =>
    request<PurgeHiddenPayload>(
      `/api/v1/users/current/curation/${id}/purge`,
      { method: "POST" },
    ),

  // --- Goals (gaka-wpb) --------------------------------------------------------
  // Backend wraps the list in {goals:[...]} — unwrap to a bare Goal[]
  // for consumers (matches getCurationRules).
  getGoals: () => unwrap<Goal[]>("/api/v1/users/current/goals", "goals", []),
  // Backend wraps single-goal responses in {goal:{...}} for POST/GET/
  // PATCH; unwrap so components see a bare Goal shape.
  getGoal: async (id: string): Promise<Goal> => {
    const raw = await request<{ goal: Goal }>(
      `/api/v1/users/current/goals/${encodeURIComponent(id)}`,
    );
    return raw.goal;
  },
  createGoal: async (body: CreateGoalBody): Promise<Goal> => {
    const raw = await request<{ goal: Goal }>(
      "/api/v1/users/current/goals",
      { method: "POST", body },
    );
    return raw.goal;
  },
  updateGoal: async (id: string, body: UpdateGoalBody): Promise<Goal> => {
    const raw = await request<{ goal: Goal }>(
      `/api/v1/users/current/goals/${encodeURIComponent(id)}`,
      { method: "PATCH", body },
    );
    return raw.goal;
  },
  deleteGoal: (id: string) =>
    request<void>(`/api/v1/users/current/goals/${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),
  // Toggle: omit `enabled` to flip, pass true/false for an idempotent
  // exact-set (defends against double-click races). Same pattern as
  // toggleCurationRule.
  toggleGoal: (id: string, enabled?: boolean) =>
    request<{ enabled: boolean }>(
      `/api/v1/users/current/goals/${encodeURIComponent(id)}/toggle`,
      {
        method: "POST",
        body: enabled === undefined ? undefined : { enabled },
      },
    ),
  // Per-goal progress. Direct GoalProgress payload (no envelope).
  getGoalProgress: (id: string) =>
    request<GoalProgress>(
      `/api/v1/users/current/goals/${encodeURIComponent(id)}/progress`,
    ),
  // Batched progress for every enabled goal. One HTTP round trip per
  // dashboard render (each tile reads its entry from the map).
  getAllGoalProgress: () =>
    request<BatchGoalProgress>("/api/v1/users/current/goals/progress"),

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

  // --- Admin: label images (gaka-myv) ---------------------------------------
  // Admin-gated (403 for non-admins). Info returns feature status + row count.
  // Regenerate takes the FE catalog snapshot so the Go side doesn't have to
  // mirror memecore/kawaii/space-marine expansions — the FE is authoritative.
  getAdminLabelImages: () =>
    request<{
      enabled: boolean;
      model: string;
      shimUrl: string;
      count: number;
      // gaka-myv: per-label metadata for the Admin table (no bytes — the
      // FE fetches images on demand via /api/v1/labels/:id/image).
      items: Array<{
        id: string;
        sizeBytes: number;
        generatedAt: string;
      }>;
      baseline: string[];
    }>("/api/v1/admin/label-images"),
  // gaka-8bz: server-side queue + WS. The response now returns per-entry
  // jobIds; the FE watches the WS for the actual lifecycle rather than
  // polling. `existing:true` means the label already had an in-flight job
  // and the caller got its handle rather than starting a duplicate.
  regenerateLabelImages: (body: {
    entries: Array<{
      id: string;
      prompt: string;
      model?: string;
      size?: string;
      seed?: number;
    }>;
    ids?: string[];
    all?: boolean;
    truncate?: boolean;
  }) =>
    request<{
      queued: number;
      jobs: Array<{ jobId: string; labelId: string; existing: boolean }>;
    }>("/api/v1/admin/label-images/regenerate", { method: "POST", body }),

  // --- Labels catalog (gaka-364.3) -----------------------------------------
  // Public GET for evaluator + admin table; admin-gated CRUD + gen-config
  // + seed.sql dump for the admin sheet editor.
  getLabelsCatalog: () =>
    request<LabelsCatalogPayload>("/api/v1/labels/catalog", { auth: false }),
  // gaka-93f.6: admin caps dashboard — users + roles/tiers + effective caps.
  getAdminUsers: () => request<AdminUsersPayload>("/api/v1/admin/users"),

  // gaka-b5n.4: linked external identities (OIDC account linking).
  getIdentities: () => request<IdentitiesPayload>("/api/v1/users/current/identities"),
  unlinkIdentity: (provider: string) =>
    request<void>(`/api/v1/users/current/identities/${encodeURIComponent(provider)}`, {
      method: "DELETE",
    }),

  // gaka-2ip Phase 1: per-user GitHub connect. GET reports {connected, login,
  // status, checkedAt} — NEVER the token. The connect flow itself is a
  // top-level browser redirect (window.location = "/auth/github/connect"), not
  // an XHR, so there's no api.ts method for it. DELETE clears the stored token.
  getGithubConnection: () => request<GithubConnection>("/api/v1/users/current/github"),
  disconnectGithub: () =>
    request<void>("/api/v1/users/current/github", { method: "DELETE" }),

  // gaka-anh Phase 2: per-user GitHub stats. Authed cache-or-sync — the server
  // serves a fresh cache or refreshes on demand, and on a GitHub rate-limit
  // returns the last-good cache with `stale: true`. NEVER carries the token.
  // The public mirror (GET /api/public/profile/:slug/github/stats) is served by
  // the public-profile feature; this method is the authed self view.
  getGithubStats: () =>
    request<GithubStatsPayload>("/api/v1/users/current/github/stats"),

  // gaka-2ud Phase 5: the PUBLIC, UNAUTH mirror served by the public-profile
  // feature (GET /api/public/profile/:slug/github/stats). Cache-only server-side
  // (never syncs), respects `public_profile_enabled`, and 404s when there's no
  // cache / the profile isn't public. Returns the SAME GithubStatsPayload as the
  // authed self view — NEVER carries the token. Consumed by the public
  // GithubCard, which silently hides on any 404 / empty payload.
  getPublicGithubStats: (slug: string) =>
    request<GithubStatsPayload>(
      `/api/public/profile/${encodeURIComponent(slug)}/github/stats`,
      { auth: false },
    ),

  adminCreateLabel: (body: Partial<LabelCatalogRow> & { id: string; kind: string; label: string; condition: unknown }) =>
    request<LabelCatalogRow>("/api/v1/admin/labels", { method: "POST", body }),
  adminUpdateLabel: (id: string, body: Partial<LabelCatalogRow> & { condition?: unknown }) =>
    request<LabelCatalogRow>(`/api/v1/admin/labels/${encodeURIComponent(id)}`, {
      method: "PATCH",
      body,
    }),
  adminDeleteLabel: (id: string) =>
    request<void>(`/api/v1/admin/labels/${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),
  adminUpdateLabelGenConfig: (systemPrompt: string) =>
    request<{ systemPrompt: string }>("/api/v1/admin/label-gen-config", {
      method: "PATCH",
      body: { systemPrompt },
    }),
  // --- Git-history backfill (gaka-vh8) --------------------------------------
  // Admin-only. Config lives in backfill_config; stats read backfill:%
  // rows from heartbeats via a partial index. The CLI runs on the
  // operator's laptop and streams heartbeats to /admin/backfill/jobs/:id/
  // heartbeats — the FE only needs the config editor + stats + WS + delete.
  getBackfillConfig: () =>
    request<{
      username: string;
      clusterGapSec: number;
      preCommitLeadSec: number;
      postCommitTailSec: number;
      heartbeatRateSec: number;
      authorEmails: string[];
      sourceTag: string;
      langMap: Record<string, string>;
      updatedAt: string;
    }>("/api/v1/admin/backfill/config"),
  patchBackfillConfig: (body: Partial<{
    clusterGapSec: number;
    preCommitLeadSec: number;
    postCommitTailSec: number;
    heartbeatRateSec: number;
    authorEmails: string[];
    sourceTag: string;
    langMap: Record<string, string>;
  }>) =>
    request<{
      username: string;
      clusterGapSec: number;
      preCommitLeadSec: number;
      postCommitTailSec: number;
      heartbeatRateSec: number;
      authorEmails: string[];
      sourceTag: string;
      langMap: Record<string, string>;
      updatedAt: string;
    }>("/api/v1/admin/backfill/config", { method: "PATCH", body }),
  // gaka-mwp-streaks: award-ledger writes + reads.
  //
  // `at` (optional, ISO-8601) is for the backfill tool on the admin
  // labels tab — server buckets against that instant instead of
  // time.Now(). Rejected if in the future.
  logAwards: (
    items: { labelId: string; periodType: "daily" | "weekly" | "monthly" }[],
    at?: string,
  ) =>
    request<{ received: number; written: number }>(
      "/api/v1/users/current/awards/log",
      { method: "POST", body: at ? { items, at } : { items } },
    ),
  getAwardStreaks: () =>
    request<Record<string, number>>("/api/v1/users/current/awards/streaks"),
  /** Debug inspector — full ledger rows for the caller (paginated by
   *  `limit`, default 500). Optionally scoped to a single labelId. */
  getAwardLedger: (opts: { label?: string; limit?: number } = {}) =>
    request<{
      rows: Array<{
        labelId: string;
        labelName: string;
        kind: string;
        periodType: "daily" | "weekly" | "monthly";
        periodStart: string;
        periodEnd: string;
        loggedAt: string;
      }>;
      limit: number;
    }>("/api/v1/users/current/awards/ledger", {
      params: {
        ...(opts.label ? { label: opts.label } : {}),
        ...(opts.limit ? { limit: opts.limit } : {}),
      },
    }),
  getPublicAwardStreaks: (slug: string) =>
    request<Record<string, number>>(
      `/api/public/profile/${encodeURIComponent(slug)}/awards/streaks`,
    ),

  // gaka-hc6.3 / gaka-hc6.4: server-side award evaluation. Replaces the
  // client-side evaluate() call. Own variant WRITES the ledger; public
  // variant is read-only for the ledger.
  getOwnAwards: () =>
    request<
      Array<{
        id: string;
        kind: "tier" | "archetype" | "tribe" | "meme" | "patch";
        label: string;
        glyph?: string;
        description: string;
        rank: number;
        tier?: string;
        condition?: unknown;
      }>
    >("/api/v1/users/current/awards"),
  getPublicAwards: (slug: string) =>
    request<
      Array<{
        id: string;
        kind: "tier" | "archetype" | "tribe" | "meme" | "patch";
        label: string;
        glyph?: string;
        description: string;
        rank: number;
        tier?: string;
        condition?: unknown;
      }>
    >(`/api/public/profile/${encodeURIComponent(slug)}/awards`),
  // gaka-hc6.5.1: historical replay. Server walks N days back, evaluates
  // each day's snapshot, writes ledger rows with at=D. Replaces the
  // per-day client-side loop that used to run in StreakBackfillSection.
  awardsBackfill: (days: number) =>
    request<{
      daysProcessed: number;
      rowsWritten: number;
      skipped: number;
      tookMs: number;
    }>("/api/v1/users/current/awards/backfill", {
      method: "POST",
      body: { days },
    }),

  getBackfillStats: () =>
    request<{
      totalRows: number;
      sources: Record<string, number>;
      oldest?: string;
      newest?: string;
      // Debug rollups added for the Synthetic heartbeat inspector on the
      // Backfill admin tab. Top-10 per axis across all backfill:% rows.
      topFiles: BackfillRollupEntry[];
      topProjects: BackfillRollupEntry[];
      topLanguages: BackfillRollupEntry[];
    }>("/api/v1/admin/backfill/stats"),
  /** Delete backfilled heartbeats. Either pass source=<tag> (must start
   *  with "backfill:") or all=true to purge every backfill:% row for the
   *  caller. Never touches real Wakatime rows (server floor). */
  deleteBackfillHeartbeats: (params: { source?: string; all?: boolean }) =>
    request<{ deleted: number }>("/api/v1/admin/backfill/heartbeats", {
      method: "DELETE",
      params: {
        source: params.source ?? "",
        all: params.all ? "true" : "",
      },
    }),

  /** Fetch the SQL dump as raw text — the caller triggers a browser
   *  download from the returned string. Not JSON. */
  adminLabelsSeedSQL: async (): Promise<string> => {
    const headers: Record<string, string> = {};
    const h = authStore.authHeader();
    if (h) headers.Authorization = h;
    const res = await fetch("/api/v1/admin/labels/seed.sql", { headers });
    if (!res.ok) {
      throw new ApiError(res.status, `seed.sql fetch failed: ${res.status}`, await res.text());
    }
    return await res.text();
  },

  // --- Avatar (gaka-9v4) -----------------------------------------------------
  // Prompt synthesis is SSE — the FE reads the ReadableStream directly via
  // useAvatarPromptStream (see features/settings/avatar/useAvatarPromptStream)
  // so it doesn't go through the JSON-envelope request() helper.
  // Regenerate + status use the standard client.
  regenerateAvatar: (body: { prompt: string; model?: string; size?: string; seed?: number }) =>
    request<{ status: string }>("/api/v1/users/current/avatar/regenerate", {
      method: "POST",
      body,
    }),
  getAvatarStatus: () =>
    request<{
      status: "none" | "pending" | "running" | "ready" | "error";
      error?: string;
      generatedAt?: string;
      updatedAt?: string;
    }>("/api/v1/users/current/avatar/status"),
};
