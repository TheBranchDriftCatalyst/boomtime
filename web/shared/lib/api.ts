// Typed fetch client for every backend endpoint. All key/URL/shape tweaks live
// here or in src/types/api.ts so backend key changes are one-line edits.
import { authStore, broadcastLogout } from "@shared/features/auth/auth";
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
  AdminJob,
  AdminJobSchedule,
  AdminJobQueue,
  ServerLogEntry,
  MetricFamily,
  IdentitiesPayload,
  GithubConnection,
  AmazonConnection,
  ReadingItemDTO,
  ReadingMonitorState,
  ReadingMonitorStatus,
  ReadingMonitorRaw,
  ReadingMonitorMode,
  CurationPatch,
  HardcoverConnection,
  HardcoverCandidate,
  NotificationDTO,
  ReadEvent,
  GithubStatsPayload,
  WidgetLinkPayload,
  WidgetLinksPayload,
  WidgetScope,
  // boom-wpb: goals feature types.
  BatchGoalProgress,
  CreateGoalBody,
  Goal,
  GoalProgress,
  UpdateGoalBody,
} from "@shared/types/api";
import type {
  LabelCatalogRow,
  LabelsCatalogPayload,
} from "@shared/features/publicprofile/labels/types";

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
        if (!res.ok) {
          // A 401/403 from the refresh ITSELF means the session is truly dead
          // (refresh token expired/revoked) — not a just-expired access token.
          // Clear + broadcast so ProtectedRoute bounces the user to /login
          // instead of leaving them on a page that silently 401s. Mirrors the
          // proactive tick in useAuth. A 5xx/network error is transient and
          // must NOT nuke the session (handled by the catch below → return
          // false), so the next request or tick can retry.
          if (res.status === 401 || res.status === 403) {
            authStore.clear();
            broadcastLogout();
          }
          return false;
        }
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

// --- Admin CLI-runner (BOOM_FEATURE_ADMIN_CLI) -------------------------------
// Wire types mirror internal/climeta/spec.go + internal/admin/cli_*.go
// verbatim. The three endpoints are admin-gated AND only registered when the
// backend feature flag is on — when off they 404, which the Commands tab
// renders as a "feature disabled" state (not an error).

export type CliClassification = "readonly" | "mutating" | "destructive";

export interface CliParam {
  name: string;
  shorthand?: string;
  usage?: string;
  type: "bool" | "string" | "int" | "stringSlice" | "enum";
  default?: string;
  enum?: string[];
  positional: boolean;
  required: boolean;
  secret: boolean;
  completable: boolean;
}

export interface CliCommandSpec {
  command: string;
  short: string;
  long?: string;
  classification: CliClassification;
  dryRunSupported: boolean;
  params: CliParam[];
}

export interface CliRunResponse {
  ok: boolean;
  // Captured output, capped at 64 KiB server-side. When capped, the string
  // itself ends with an inline "… [output truncated]" marker — there is no
  // separate truncation field. NOT scrubbed/redacted (deliberate: admin-only
  // console); render verbatim.
  output: string;
  exitError: string; // "" on success
  dryRun: boolean;
  durationMs: number;
}

export interface CliCompleteResponse {
  suggestions: { value: string; description?: string }[];
  directive: {
    noFileComp: boolean;
    noSpace: boolean;
    noSort: boolean;
    keepOrder: boolean;
    error: boolean;
  };
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

  // boom-93f.1.1: public client-config advertisement. Unauthenticated; read
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

  // Change password (boom-6jm). Server verifies currentPassword, hashes the
  // new one with argon2id, and revokes every other refresh token for the
  // owner — the caller's access token stays valid so no immediate re-login.
  changePassword: (body: { currentPassword: string; newPassword: string }) =>
    request<void>("/api/v1/users/current/password", {
      method: "POST",
      body,
    }),

  // Encrypted-at-rest imported Wakatime API key (boom-6jm.2).
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

  // Per-user IANA timezone (boom-dg7).
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

  // Public profile (boom-6jm.1). GET returns the caller's toggle + slug so
  // Settings can render the current state and the Sidebar can conditionally
  // show a "Public profile" nav link. PUT writes { enabled, slug }; the
  // server enforces the slug regex, blocks reserved names, and returns 409
  // on slug conflict — surfaced as an ApiError with status=409 that the
  // form maps to an inline "already taken" message.
  getPublicProfile: () =>
    request<{
      enabled: boolean;
      slug: string | null;
      cardTheme: string;
      cardTagline: string;
    }>("/api/v1/users/current/profile"),
  // gaka social-card: cardTheme / cardTagline are optional — omitting them
  // leaves the stored values untouched (a toggle-only save won't clobber the
  // tagline, and vice versa).
  savePublicProfile: (body: {
    enabled: boolean;
    slug: string;
    cardTheme?: string;
    cardTagline?: string;
  }) =>
    request<{
      enabled: boolean;
      slug: string | null;
      cardTheme: string;
      cardTagline: string;
    }>("/api/v1/users/current/profile", { method: "PUT", body }),
  // Public payload — no auth. Used by the /p/:slug dashboard route.
  // boom-174.7: optional `days` re-scopes the STATS window (server clamps to
  // 1..365, default 60). Labels/awards come from a separate endpoint that
  // stays on the canonical window, so re-scoping never touches them.
  getPublicDashboard: (slug: string, days?: number) =>
    request<PublicDashboardPayload>(
      `/api/public/profile/${encodeURIComponent(slug)}` +
        (days ? `?days=${days}` : ""),
      { auth: false },
    ),

  // Dashboard layout persistence (boom-keb). Per-user, per-scope.
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

  // boom-1l9: AI-assistance per-day metrics + range summary (input/output
  // tokens, AI vs human line changes, distinct sessions, latest plan).
  getAIActivity: (params: RangeParams) =>
    request<AIActivityPayload>("/api/v1/users/current/stats/ai", { params }),

  // boom-yfg: total + per-project lines of code (current snapshot) plus a
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

  // --- Entity Explorer (boom-90x) --------------------------------------------

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

  // --- Canonical-entity PINS (boom-canon) ------------------------------------
  // A "pin" is a curation rule with action="pin": it forces its (axis, value)
  // to always get its own slice/bar and never fall into the bucket "Other"
  // roll-up. The backend query engine auto-applies pins at group time, so
  // these are just thin wrappers over the same create/list/delete curation
  // endpoints (mirroring addCurationRule / getCurationRules / deleteCurationRule).
  //
  // Pins are always exact matches with no newValue; the body is fixed so
  // callers only supply (axis, value).
  pinValue: (axis: string, value: string) =>
    request<AddCurationRulePayload>("/api/v1/users/current/curation", {
      method: "POST",
      body: {
        axis,
        action: "pin",
        matchType: "exact",
        matchValue: value,
        applyAtIngest: false,
      },
    }),

  // Remove a pin by its curation-rule id (identical to deleteCurationRule, but
  // named for the pin call sites so intent is legible at the toggle).
  unpinValue: (ruleId: number) =>
    request<void>(`/api/v1/users/current/curation/${ruleId}`, {
      method: "DELETE",
    }),

  // The current pins — the curation rules list filtered to action==="pin".
  // Reuses the same { rules: [...] } envelope as getCurationRules.
  listPins: async (): Promise<CurationRule[]> => {
    const rules = await unwrap<CurationRule[]>(
      "/api/v1/users/current/curation",
      "rules",
      [],
    );
    return rules.filter((r) => r.action === "pin");
  },

  // boom-dfd: pause / resume a curation rule without deleting it. Body is
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

  // boom-cr4 + boom-due: preview a destructive curation action. The response
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

  // boom-cr4: DESTRUCTIVELY apply a rename rule — rewrites raw heartbeat rows
  // (UPDATE) and removes the rule row itself, atomically. Rejects non-rename
  // rules with 400. Returned sqlRun matches the preview's sqlPlanned verbatim.
  applyCurationRule: (id: number) =>
    request<ApplyRenamePayload>(
      `/api/v1/users/current/curation/${id}/apply`,
      { method: "POST" },
    ),

  // boom-due: DESTRUCTIVELY purge a hide rule — DELETEs every heartbeat row
  // the rule matches, then removes the rule row itself, atomically. Rejects
  // non-hide rules with 400. Data-obliterating: the FE gates this behind a
  // "type rule id N to confirm" input to prevent muscle-memory Enter
  // presses. Returned sqlRun matches the preview's sqlPlanned verbatim.
  purgeCurationRule: (id: number) =>
    request<PurgeHiddenPayload>(
      `/api/v1/users/current/curation/${id}/purge`,
      { method: "POST" },
    ),

  // --- Goals (boom-wpb) --------------------------------------------------------
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

  // --- Admin: label images (boom-myv) ---------------------------------------
  // Admin-gated (403 for non-admins). Info returns feature status + row count.
  // Regenerate takes the FE catalog snapshot so the Go side doesn't have to
  // mirror memecore/kawaii/space-marine expansions — the FE is authoritative.
  getAdminLabelImages: () =>
    request<{
      enabled: boolean;
      model: string;
      shimUrl: string;
      count: number;
      // boom-myv: per-label metadata for the Admin table (no bytes — the
      // FE fetches images on demand via /api/v1/labels/:id/image).
      items: Array<{
        id: string;
        sizeBytes: number;
        generatedAt: string;
      }>;
      baseline: string[];
      // worker-topology decoupling (boom-8bz follow-up): which transport is
      // actually executing regens. "inprocess" = the server's own pool;
      // "rabbitmq" = a separate boomtime-worker pod via the AMQP queue.
      broker: "inprocess" | "rabbitmq";
      // Only present when broker === "rabbitmq" AND the depth check
      // succeeded (best-effort — see AdminLabelImagesInfo).
      queueDepth?: number;
      // Only present when broker === "rabbitmq" AND BOOM_RABBITMQ_MGMT_URL
      // is configured.
      mgmtUrl?: string;
    }>("/api/v1/admin/label-images"),
  // boom-8bz: server-side queue + WS. The response now returns per-entry
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

  // Per-label regen status from the DB job queue (boom-hney Stage 3). Under
  // BOOM_JOBS_UNIFIED the admin tab polls this instead of the imagejobs WS.
  // Returns the latest job per label (queued/running always; done/error within
  // their retention window). `jobs: []` when the jobs subsystem is off.
  getLabelImageStatus: () =>
    request<{
      jobs: Array<{
        labelId: string;
        status: "queued" | "running" | "done" | "error";
        error?: string;
        startedAt?: string;
        finishedAt?: string;
      }>;
    }>("/api/v1/admin/label-images/status"),

  // --- Labels catalog (boom-364.3) -----------------------------------------
  // Public GET for evaluator + admin table; admin-gated CRUD + gen-config
  // + seed.sql dump for the admin sheet editor.
  getLabelsCatalog: () =>
    request<LabelsCatalogPayload>("/api/v1/labels/catalog", { auth: false }),
  // boom-93f.6: admin caps dashboard — users + roles/tiers + effective caps.
  getAdminUsers: () => request<AdminUsersPayload>("/api/v1/admin/users"),

  // --- Admin: background jobs (boom-hney) ------------------------------------
  // Admin-gated (403 for non-admins). The list + schedules endpoints wrap
  // their result in a single-key envelope ({ jobs }, { schedules }); unwrap to
  // the bare array so the Jobs tab consumes a flat list. trigger/retry both
  // return the affected job id.
  // Admin › Books diagnostics (boom-books): raw source dump for Audible/Kindle.
  // source=liberation (boom-w20s.19) additionally accepts `asin` to pin the
  // sweep to one title, and its probes carry verdict/detail — they answer a
  // protocol question rather than dumping a response.
  getBooksDiagnostics: (params: { source: string; asin?: string }) =>
    request<{
      source: string;
      marketplace: string;
      asin?: string;
      probes: Array<{
        name: string;
        endpoint: string;
        status: number;
        ok: boolean;
        error?: string;
        body?: unknown;
        bodyText?: string;
        verdict?: "pass" | "warn" | "fail" | "skip";
        detail?: string;
      }>;
    }>(buildUrl("/api/v1/admin/books/diagnostics", params)),

  // --- catalyst-books liberation (boom-w20s) --------------------------------
  // The Libation rebuild. All mutations ENQUEUE a background job — a liberation
  // is minutes of download plus minutes of remux, so none of these block on the
  // work itself. Every route 404s when BOOM_FEATURE_BOOKS_LIBERATION is off (or
  // no library path is configured), which is how the UI decides whether to show
  // the controls at all.
  getLiberationStatus: () =>
    request<{
      counts: Record<string, number>;
      pending: number;
      // Titles the sweep will no longer pick up on its own. Count only — the
      // rows come from getLiberationExcluded when someone opens the list.
      excluded: number;
      libraryPath: string;
    }>("/api/v1/books/liberation/status"),

  // The give-up set: what liberation stopped trying, and why. `retryable`
  // separates "we ran out of attempts" (worth another go) from a verdict about
  // the title itself — a denied or non-audio asset will refuse identically
  // every time, so retrying it only spends another request against Amazon.
  getLiberationExcluded: () =>
    request<{
      items: Array<{
        asin: string;
        title: string;
        author?: string;
        status: string;
        error?: string;
        attempts: number;
        retryable: boolean;
      }>;
    }>("/api/v1/books/liberation/excluded"),

  // force=true re-liberates a book the idempotency check would otherwise skip.
  liberateBook: (externalId: string, force = false) =>
    request<{ enqueued: boolean; jobId: number; asin: string }>(
      buildUrl(
        `/api/v1/books/items/${encodeURIComponent(externalId)}/liberate`,
        force ? { force: "true" } : {},
      ),
      { method: "POST" },
    ),

  // deleteFile defaults to FALSE: forgetting the state is cheap, deleting a
  // 600MB file the user has to re-download is not.
  forgetLiberation: (externalId: string, deleteFile = false) =>
    request<{ forgotten: boolean; fileDeleted: boolean }>(
      buildUrl(
        `/api/v1/books/items/${encodeURIComponent(externalId)}/liberate`,
        deleteFile ? { deleteFile: "true" } : {},
      ),
      { method: "DELETE" },
    ),

  // `pending` comes back so the UI can say how many books were just queued.
  sweepLiberation: (body: { limit?: number; force?: boolean } = {}) =>
    request<{ enqueued: boolean; jobId: number; pending: number }>(
      "/api/v1/books/liberate/sweep",
      { method: "POST", body: JSON.stringify(body) },
    ),

  // Admin › Books › reading monitor (boom-books): thin control over the
  // SERVER-side persistent engine. GET reads its live state; PUT flips the
  // on/off switch and/or the toast mode. The panel polls GET lightly for status
  // display only — the poll cadence itself runs server-side now, not here.
  //
  // rm2 · `calibrate` starts (true) / cancels (false) the temporary high-
  // fidelity diagnostic (calibration) window. It's orthogonal to enabled/mode,
  // so the FE sends it alone. The PUT returns the full state either way.
  getReadingMonitor: () =>
    request<ReadingMonitorState>("/api/v1/admin/books/reading-monitor"),
  setReadingMonitor: (body: {
    enabled?: boolean;
    mode?: ReadingMonitorMode;
    calibrate?: boolean;
  }) =>
    request<ReadingMonitorState>("/api/v1/admin/books/reading-monitor", {
      method: "PUT",
      body,
    }),
  // rm2 · user-scoped lightweight beacon (requireAuth, NOT admin-gated) for the
  // global nav indicator — polled ~15s from the shared header on every page.
  getReadingMonitorStatus: () =>
    request<ReadingMonitorStatus>("/api/v1/books/reading-monitor/status"),
  // rm2 · admin-gated raw sample feed from BOTH reading sources — the human-
  // readable complement to the Grafana cadence board.
  getReadingMonitorRaw: () =>
    request<ReadingMonitorRaw>("/api/v1/admin/books/reading-monitor/raw"),

  getAdminJobs: (params?: { status?: string; kind?: string; limit?: number }) =>
    unwrap<AdminJob[]>(buildUrl("/api/v1/admin/jobs", params), "jobs", []),
  // boom-metrics: the Prometheus gathered view (router RED, outbound RED,
  // limiter + external-API business counters, Go/process runtime). Same
  // registry served at /metrics for Grafana; here it's Gather()ed into
  // {families:[...]} for the in-app tab. Generic — any newly-registered
  // collector appears with zero FE wiring. `names` filters by name prefix.
  getAdminMetrics: (params?: { names?: string }) =>
    unwrap<MetricFamily[]>(
      buildUrl("/api/v1/admin/metrics", params),
      "families",
      [],
    ),
  getAdminJobSchedules: () =>
    unwrap<AdminJobSchedule[]>(
      "/api/v1/admin/jobs/schedules",
      "schedules",
      [],
    ),
  // boom-hney: per-kind queue overview — live depth, running/max headroom,
  // trailing-hour throughput + fail ratio. Backs the queue cards atop the Jobs
  // tab; polled so the limiter's back-pressure is visible in real time.
  getJobQueues: () =>
    unwrap<AdminJobQueue[]>("/api/v1/admin/jobs/queues", "queues", []),
  triggerAdminJob: (kind: string) =>
    request<{ id: number }>("/api/v1/admin/jobs/trigger", {
      method: "POST",
      body: { kind },
    }),
  retryAdminJob: (id: number) =>
    request<{ id: number }>(`/api/v1/admin/jobs/${id}/retry`, {
      method: "POST",
    }),
  // Cooperatively cancel a queued/running job. cancelled=false when the job had
  // already reached a terminal state; wasRunning=true when a live handler's
  // context was signalled (in-process, this pod).
  cancelJob: (id: number) =>
    request<{ cancelled: boolean; wasRunning: boolean }>(
      `/api/v1/admin/jobs/${id}/cancel`,
      { method: "POST" },
    ),

  // Persisted per-job log stream (boom-hney). A FINISHED job's live LogHub lines
  // are gone once the in-memory ring rolls over; these are the durable copy
  // flushed to object storage on completion. A 404 (nothing stored — job never
  // captured, logs deleted, or S3 off) resolves to [] so the viewer shows its
  // empty state rather than erroring.
  getJobLogs: async (id: number): Promise<ServerLogEntry[]> => {
    try {
      return await unwrap<ServerLogEntry[]>(
        `/api/v1/admin/jobs/${id}/logs`,
        "entries",
        [],
      );
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) return [];
      throw e;
    }
  },
  // Delete ONLY the stored log object for a job — the jobs-table row is kept.
  deleteJobLogs: (id: number) =>
    request<{ deleted: boolean }>(`/api/v1/admin/jobs/${id}/logs`, {
      method: "DELETE",
    }),
  // Bulk-clear stored job logs: a whole kind (`{ kind }`) or every kind (no arg).
  // Object storage only — the jobs-table rows are never touched. Returns the
  // count of stored log objects removed.
  clearJobLogs: (params?: { kind?: string }) =>
    request<{ deleted: number }>(
      buildUrl("/api/v1/admin/jobs/logs", params),
      { method: "DELETE" },
    ),

  // --- Admin CLI-runner (BOOM_FEATURE_ADMIN_CLI) -----------------------------
  // All three 404 when the backend feature flag is off (routes not
  // registered) — the Commands tab maps that to a friendly disabled state.

  // Introspected catalog of every web-runnable command (registry ∩
  // annotation ∩ availability, enforced server-side).
  getCliSpec: () =>
    request<{ commands: CliCommandSpec[] }>("/api/v1/admin/cli/spec"),

  // Run ONE allowlisted command in-process. Positional params travel INSIDE
  // `flags` keyed by param name (the binder routes them by the spec's
  // positional marker). Mutating semantics: omitting the "dry-run" key runs
  // a dry-run when the command supports it; applying requires
  // flags["dry-run"]=false AND confirm === command. Mutating commands
  // WITHOUT dry-run support require confirm === command on every run.
  // HTTP 200 even when ok:false — a failing command is a valid run outcome.
  runCliCommand: (body: {
    command: string;
    flags: Record<string, unknown>;
    confirm?: string;
  }) =>
    request<CliRunResponse>("/api/v1/admin/cli/run", {
      method: "POST",
      body,
    }),

  // Cobra-powered autocomplete for one completable param. `args` carries the
  // values of prior POSITIONAL params (in order) so contextual completers
  // behave exactly as under a shell <TAB>; `flag` names the flag being
  // completed (omit for a positional). enum params complete client-side from
  // the spec — the FE never calls this for them.
  completeCli: (body: {
    command: string;
    args?: string[];
    flag?: string;
    toComplete: string;
  }) =>
    request<CliCompleteResponse>("/api/v1/admin/cli/complete", {
      method: "POST",
      body,
    }),

  // boom-b5n.4: linked external identities (OIDC account linking).
  getIdentities: () => request<IdentitiesPayload>("/api/v1/users/current/identities"),
  unlinkIdentity: (provider: string) =>
    request<void>(`/api/v1/users/current/identities/${encodeURIComponent(provider)}`, {
      method: "DELETE",
    }),

  // boom-2ip Phase 1: per-user GitHub connect. GET reports {connected, login,
  // status, checkedAt} — NEVER the token. The connect flow itself is a
  // top-level browser redirect (window.location = "/auth/github/connect"), not
  // an XHR, so there's no api.ts method for it. DELETE clears the stored token.
  getGithubConnection: () => request<GithubConnection>("/api/v1/users/current/github"),
  disconnectGithub: () =>
    request<void>("/api/v1/users/current/github", { method: "DELETE" }),

  // Amazon device connect (catalyst-books + catalyst-audiobooks share ONE link).
  // Paste-the-maplanding-URL flow: start → open authorizeUrl → paste redirect →
  // complete. Or import a .audible auth file. The credential never leaves the server.
  getAmazonConnection: () => request<AmazonConnection>("/api/v1/amazon"),
  amazonConnectStart: (body: { marketplace?: string }) =>
    request<{ authorizeUrl: string; session: string }>("/api/v1/amazon/connect/start", {
      method: "POST",
      body,
    }),
  amazonConnectComplete: (body: { session: string; redirectUrl: string }) =>
    request<void>("/api/v1/amazon/connect/complete", { method: "POST", body }),
  amazonImportAuth: (authFile: unknown) =>
    request<void>("/api/v1/amazon/connect/import", { method: "POST", body: { authFile } }),
  disconnectAmazon: () => request<void>("/api/v1/amazon", { method: "DELETE" }),

  // Audible ingest (catalyst-audiobooks). syncAudible runs a forward delta
  // synchronously and returns the item count; backfillAudible enqueues the
  // one-shot all-time sweep on the jobs worker (returns the job id). getBooksItems
  // lists the siloed reading_items so the card can show a synced count.
  syncAudible: () =>
    request<{ synced: number; source: string }>("/api/v1/amazon/audible/sync", { method: "POST" }),
  backfillAudible: () =>
    request<{ enqueued: boolean; jobId: number }>("/api/v1/amazon/audible/backfill", {
      method: "POST",
    }),
  // Kindle ingest (catalyst-books) — same shapes as Audible: the shared Amazon
  // device feeds Kindle too. syncKindle runs a forward delta synchronously and
  // returns the item count; backfillKindle enqueues the all-time sweep on the
  // jobs worker (returns the job id).
  syncKindle: () =>
    request<{ synced: number; source: string }>("/api/v1/kindle/sync", { method: "POST" }),
  backfillKindle: () =>
    request<{ enqueued: boolean; jobId: number }>("/api/v1/kindle/backfill", {
      method: "POST",
    }),
  getBooksItems: (source?: string) =>
    request<{ items: ReadingItemDTO[] }>(
      `/api/v1/books/items${source ? `?source=${encodeURIComponent(source)}` : ""}`,
    ),

  // Per-book curation override (boom-books Stage 5). PATCHes the status / rating
  // / finished-date that maps to Hardcover — the override layer, sticky against
  // Amazon re-derivation. Returns the updated EFFECTIVE reading row (override ??
  // derived). A reading row has no numeric id — it's keyed by (source,
  // external_id) — so we address it by externalId with source as a required
  // query param (owner comes from auth). status is one of the 5 canonical
  // values; rating/finishedAt accept null to CLEAR an override (fall back to the
  // derived layer). Only-present keys are sent, so a rating edit never disturbs
  // the status override and vice-versa.
  setBookCuration: (item: ReadingItemDTO, patch: CurationPatch) =>
    request<ReadingItemDTO>(
      buildUrl(
        `/api/v1/books/items/${encodeURIComponent(item.externalId)}/curation`,
        { source: item.source },
      ),
      { method: "PATCH", body: patch },
    ),

  // Per-row "sync to Hardcover now" — push-only, INLINE (bypasses the job queue):
  // re-mirrors the row's CURRENT effective state to Hardcover and returns the
  // updated row (hardcover_status advanced, so the divergence badge clears). 409 if
  // the book isn't matched. Falls back to 202 {enqueued} only if the inline service
  // is unwired server-side — callers should treat a missing status field as success.
  pushBookToHardcover: (item: ReadingItemDTO) =>
    request<ReadingItemDTO>(
      buildUrl(
        `/api/v1/books/items/${encodeURIComponent(item.externalId)}/push`,
        { source: item.source },
      ),
      { method: "POST" },
    ),

  // Delete one read from a book's history (reading_events) + propagate the delete
  // to Hardcover when it originated there. Returns whether the Hardcover-side
  // delete also succeeded.
  deleteReadingEvent: (id: number) =>
    request<{ deleted: boolean; hardcoverDeleted: boolean }>(
      buildUrl(`/api/v1/books/reads/${id}`),
      { method: "DELETE" },
    ),

  // Durable notifications (migration 00079): replayed on session start so events
  // fired while offline aren't dropped. markNotificationsRead flips all unread.
  getNotifications: () =>
    request<{ notifications: NotificationDTO[] | null; unreadCount: number }>(
      buildUrl("/api/v1/notifications"),
    ),
  markNotificationsRead: () =>
    request<{ marked: number }>(buildUrl("/api/v1/notifications/read"), {
      method: "POST",
    }),

  // Book detail panel: all editions of one canonical Work (rows sharing a
  // hardcover_book_id, or amazon_asin for unmatched siblings). Keyed off the
  // clicked row's hardcoverBookId when matched, else its amazonAsin/externalId.
  getBookWork: (item: ReadingItemDTO) =>
    request<{ editions: ReadingItemDTO[]; reads: ReadEvent[] }>(
      buildUrl("/api/v1/books/work", {
        bookId: item.hardcoverBookId ?? undefined,
        asin:
          item.hardcoverBookId == null
            ? item.amazonAsin || item.externalId
            : undefined,
      }),
    ),

  // Manual match-fixer. hardcoverSearch live-queries Hardcover's catalog (Typesense)
  // for the autocomplete; setBookManualMatch applies a chosen candidate to the row
  // (writes a "manual" linkage). Both owner-scoped; search is read-only.
  hardcoverSearch: (q: string) =>
    request<{ candidates: HardcoverCandidate[] }>(
      buildUrl("/api/v1/hardcover/search", { q }),
    ),
  setBookManualMatch: (
    item: ReadingItemDTO,
    pick: { hardcoverBookId: number; editionId?: number; slug?: string },
  ) =>
    request<ReadingItemDTO>(
      buildUrl(
        `/api/v1/books/items/${encodeURIComponent(item.externalId)}/match`,
        { source: item.source },
      ),
      { method: "POST", body: pick },
    ),

  // Hardcover connect (catalyst-books PUSH target). Paste-a-bearer-token flow:
  // the server validates the token with a me{} query before storing it, and it
  // NEVER leaves the server after. GET reports presence/status only.
  getHardcoverConnection: () => request<HardcoverConnection>("/api/v1/hardcover"),
  connectHardcover: (body: { token: string }) =>
    request<void>("/api/v1/hardcover/connect", { method: "POST", body }),
  disconnectHardcover: () => request<void>("/api/v1/hardcover", { method: "DELETE" }),
  // Hardcover on-demand pipeline steps (catalyst-books). Both READ-ONLY / safe:
  // they enqueue a job on the worker (returns the job id) and NEVER write to the
  // Hardcover shelf — outbound writes stay dry-run-gated. matchHardcover resolves
  // unmatched reading_items against Hardcover's catalog; pullHardcover pulls the
  // user's current Hardcover reading state IN.
  // force=true re-checks EVERY unmatched book, ignoring the 30-day negative-cache
  // window (rows the ladder previously proved unmatchable) — use after curating on
  // Hardcover. Still read-only. Omitted → the normal windowed sweep.
  matchHardcover: (opts?: { force?: boolean }) =>
    request<{ enqueued: boolean; jobId: number }>(
      `/api/v1/hardcover/match${opts?.force ? "?force=1" : ""}`,
      { method: "POST" },
    ),
  pullHardcover: () =>
    request<{ enqueued: boolean; jobId: number }>("/api/v1/hardcover/pull", {
      method: "POST",
    }),
  // books-sync-all: one orchestrator job chaining audible-sync -> kindle-sync ->
  // hardcover-match -> hardcover-pull, in dependency order.
  syncAllBooks: () =>
    request<{ enqueued: boolean; jobId: number }>("/api/v1/books/sync-all", {
      method: "POST",
    }),

  // boom-anh Phase 2: per-user GitHub stats. Authed cache-or-sync — the server
  // serves a fresh cache or refreshes on demand, and on a GitHub rate-limit
  // returns the last-good cache with `stale: true`. NEVER carries the token.
  // The public mirror (GET /api/public/profile/:slug/github/stats) is served by
  // the public-profile feature; this method is the authed self view.
  getGithubStats: () =>
    request<GithubStatsPayload>("/api/v1/users/current/github/stats"),

  // boom-2ud Phase 5: the PUBLIC, UNAUTH mirror served by the public-profile
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
  // boom-mwp-streaks: award-ledger writes + reads.
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

  // boom-hc6.3 / boom-hc6.4: server-side award evaluation. Replaces the
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
  // boom-hc6.5.1: historical replay. Server walks N days back, evaluates
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

  // --- Avatar (boom-9v4) -----------------------------------------------------
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
