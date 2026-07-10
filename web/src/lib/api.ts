// Typed fetch client for every backend endpoint. All key/URL/shape tweaks live
// here or in src/types/api.ts so backend key changes are one-line edits.
import { authStore } from "@/lib/auth";
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
  CurationRulesPayload,
  CancelImportPayload,
  HeartbeatAxis,
  HeartbeatFilters,
  HeartbeatGroupPayload,
  HeartbeatListPayload,
  ImportConfigPayload,
  ImportJobDetailPayload,
  ImportJobsListPayload,
  ImportRequest,
  ImportStatusPayload,
  SubmitImportResponse,
  WakatimeRangePayload,
  LeaderboardEntry,
  LeaderboardsPayload,
  MomentumPayload,
  ProjectListPayload,
  ProjectStatistics,
  PunchcardPayload,
  RangeParams,
  SessionsPayload,
  StatsParams,
  StatsPayload,
  StatusBarPayload,
  StoredApiToken,
  TagsPayload,
  TimelinePayload,
  TimelineRange,
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

function buildUrl(path: string, params?: Params): string {
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

  // Backend emits hakatime's raw TimelinePayload: { timelineLangs: { lang:
  // [{ tName, tRangeStart, tRangeEnd }] } }. Normalize to { langs: {...} }.
  getTimeline: async (params: {
    start: string;
    end: string;
    timeLimit?: number;
  }): Promise<TimelinePayload> => {
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

  getStatusBar: () =>
    request<StatusBarPayload>("/api/v1/users/current/statusbar/today"),

  // --- Council "big-bet" analytics -------------------------------------------

  getPunchcard: (params: { start: string; end: string; timeLimit?: number }) =>
    request<PunchcardPayload>("/api/v1/users/current/stats/punchcard", {
      params,
    }),

  getSessions: (params: { start: string; end: string; timeLimit?: number }) =>
    request<SessionsPayload>("/api/v1/users/current/stats/sessions", {
      params,
    }),

  getMomentum: (params: { start: string; end: string; top?: number }) =>
    request<MomentumPayload>("/api/v1/users/current/stats/momentum", {
      params,
    }),

  // --- Projects / Tags -------------------------------------------------------

  getProject: (project: string, params: StatsParams) =>
    request<ProjectStatistics>(
      `/api/v1/users/current/projects/${encodeURIComponent(project)}`,
      { params },
    ),

  getTagStats: (tag: string, params: StatsParams) =>
    request<ProjectStatistics>(
      `/api/v1/users/current/tags/${encodeURIComponent(tag)}`,
      { params },
    ),

  getUserProjects: (params: RangeParams) =>
    request<ProjectListPayload>("/api/v1/projects", { params }),

  getUserTags: () => request<TagsPayload>("/api/v1/tags"),

  getProjectTags: (project: string) =>
    request<TagsPayload>(
      `/api/v1/projects/${encodeURIComponent(project)}/tags`,
    ),

  setProjectTags: (project: string, tags: string[]) =>
    request<TagsPayload>(
      `/api/v1/projects/${encodeURIComponent(project)}/tags`,
      { method: "POST", body: { tags } },
    ),

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

  checkImportStatus: (body: ImportRequest) =>
    request<ImportStatusPayload>("/import/status", { method: "POST", body }),

  // First-class import jobs.
  getImportJobs: () => request<ImportJobsListPayload>("/import/jobs"),

  getImportJob: (id: number) =>
    request<ImportJobDetailPayload>(`/import/jobs/${id}`),

  cancelImportJob: (id: number) =>
    request<CancelImportPayload>(`/import/jobs/${id}/cancel`, {
      method: "POST",
    }),

  // --- Derived-data health (gap_seconds + rollup) ----------------------------

  getDerivedStatus: () =>
    request<DerivedStatus>("/api/v1/users/current/derived/status"),

  resyncDerived: () =>
    request<DerivedStatus>("/api/v1/users/current/derived/resync", {
      method: "POST",
    }),

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
    filters?: HeartbeatFilters;
  }) =>
    request<HeartbeatGroupPayload>("/api/v1/users/current/heartbeats/group", {
      params: {
        groupBy: opts.groupBy,
        start: opts.start,
        end: opts.end,
        ...(opts.filters ?? {}),
      },
    }),

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

  getCurationRules: () =>
    request<CurationRulesPayload>("/api/v1/users/current/curation"),

  addCurationRule: (body: AddCurationRuleBody) =>
    request<AddCurationRulePayload>("/api/v1/users/current/curation", {
      method: "POST",
      body,
    }),

  deleteCurationRule: (id: number) =>
    request<void>(`/api/v1/users/current/curation/${id}`, {
      method: "DELETE",
    }),
};
