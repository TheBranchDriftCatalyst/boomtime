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
  MomentumPayload,
  ProjectListPayload,
  ProjectStatistics,
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

async function request<T>(path: string, opts: RequestOpts = {}): Promise<T> {
  const { method = "GET", params, body, auth = true } = opts;
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
  // Each accumulated filter is sent as its own query param.
  groupHeartbeats: (opts: {
    groupBy: HeartbeatAxis;
    start: string;
    end: string;
    timeLimit?: number;
    filters?: HeartbeatFilters;
  }) =>
    request<HeartbeatGroupPayload>("/api/v1/users/current/heartbeats/group", {
      params: {
        groupBy: opts.groupBy,
        start: opts.start,
        end: opts.end,
        timeLimit: opts.timeLimit,
        ...(opts.filters ?? {}),
      },
    }),

  // Most-recent heartbeat marker, for the import "backfill from last" button.
  getLatestHeartbeat: () =>
    request<LatestHeartbeatPayload>(
      "/api/v1/users/current/heartbeats/latest",
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
