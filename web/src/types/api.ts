// Centralized API types mirroring the Go backend JSON payloads.
// JSON key naming follows hakatime's noPrefixOptions (drop the lowercase type
// prefix, e.g. pName -> name). Where a key is uncertain it is flagged with a
// `// TODO verify key` comment so it can be reconciled with the backend.

// ---------------------------------------------------------------------------
// Auth
// ---------------------------------------------------------------------------

export interface AuthResponse {
  token: string;
  tokenExpiry: string; // ISO timestamp
  tokenUsername: string;
}

export interface Credentials {
  username: string;
  password: string;
}

export interface CreateTokenResponse {
  apiToken: string;
}

// Normalized shape returned by api.getTokens(). The backend emits hakatime's
// raw StoredApiToken (default aeson: tknId/tknName/tknDesc/lastUsage); api.ts
// maps it to these ergonomic keys. `id` is the base64(uuid) token id.
export interface StoredApiToken {
  id: string;
  lastUsage: string | null;
  name: string | null;
  desc: string | null;
}

export interface CurrentUser {
  data: {
    full_name: string | null;
    email: string | null;
    photo: string | null;
  };
}

// GET /api/v1/users/current/derived/status — health of the precomputed
// gap_seconds column + hb_rollup_daily rollup vs the raw heartbeats.
export interface DerivedStatus {
  heartbeats: number;
  gapPopulated: number;
  gapMissing: number;
  rollupRows: number;
  rollupSeconds: number;
  rawSeconds: number;
  inSync: boolean;
  heartbeatsBytes: number;
  rollupBytes: number;
  dbBytes: number;
}

// ---------------------------------------------------------------------------
// Stats / Projects
// ---------------------------------------------------------------------------

export interface ResourceStats {
  name: string;
  totalSeconds: number;
  totalPct: number;
  totalDaily: number[];
  pctDaily: number[];
}

export interface StatsPayload {
  startDate: string;
  endDate: string;
  totalSeconds: number;
  dailyAvg: number;
  dailyTotal: number[];
  projects: ResourceStats[];
  languages: ResourceStats[];
  platforms: ResourceStats[];
  machines: ResourceStats[];
  editors: ResourceStats[];
  // Category time-series (coding/debugging/writing/…). Backend is adding this
  // alongside the other resource dimensions. Optional so the UI degrades
  // gracefully until it lands. TODO verify keys against backend report.
  categories?: ResourceStats[];
  categoriesCount?: number;
  // True distinct counts (the lists above are capped to top-N + an "Other" bucket).
  projectsCount: number;
  languagesCount: number;
  platformsCount: number;
  machinesCount: number;
  editorsCount: number;
}

// --- Council "big-bet" analytics endpoints -----------------------------------
// Bound to the backend agent's intended contract; separate GET endpoints under
// /api/v1/users/current/stats. TODO verify keys against backend report.

// GET stats/punchcard -> 7x24 day-of-week x hour-of-day grid (UTC).
export interface PunchcardCell {
  dow: number; // 0=Sun .. 6=Sat
  hour: number; // 0..23, UTC
  seconds: number;
}
export interface PunchcardPayload {
  cells: PunchcardCell[];
  maxSeconds: number;
  totalSeconds: number;
}

// GET stats/sessions -> deep-work focus sessions (runs between >timeLimit gaps).
export interface SessionsSummary {
  count: number;
  totalSeconds: number;
  avgSeconds: number;
  maxSeconds: number;
  medianSeconds: number;
}
export interface SessionsDaily {
  date: string; // YYYY-MM-DD
  sessions: number;
  totalSeconds: number;
  longestSeconds: number;
}
export interface SessionsHistogramBin {
  label: string;
  count: number;
}
export interface SessionsPayload {
  summary: SessionsSummary;
  daily: SessionsDaily[];
  histogram: SessionsHistogramBin[];
}

// GET stats/momentum?top=N -> project x week activity grid.
export interface MomentumProject {
  name: string;
  weekly: number[]; // seconds per week, aligned to `weeks`
  totalSeconds: number;
}
export interface MomentumPayload {
  weeks: string[]; // ISO Monday-start dates, one per column
  projects: MomentumProject[];
}

export interface ProjectStatistics {
  startDate: string;
  endDate: string;
  totalSeconds: number;
  dailyTotal: number[];
  languages: ResourceStats[];
  files: ResourceStats[];
  weekDay: ResourceStats[];
  hour: ResourceStats[];
  languagesCount: number;
  filesCount: number;
  // Authoring/reading + branch/breadth fields the backend agent is adding.
  // Optional so the UI compiles and degrades gracefully until they land.
  // TODO verify keys against backend report.
  writeSeconds?: number;
  readSeconds?: number;
  dailyWriteRatio?: number[]; // per-day write/(write+read), 0..1, aligned to dailyTotal
  branches?: ResourceStats[]; // top-N + "Other (N more)" like other resource lists
  branchesCount?: number; // true distinct branch count
  dailyEntities?: number[]; // distinct files touched per day, aligned to dailyTotal
}

// Normalized shape returned by api.getTimeline(). Backend emits hakatime's raw
// { timelineLangs: { <lang>: [{ tName, tRangeStart, tRangeEnd }] } }; api.ts
// maps it to { langs: { <lang>: [{ name, rangeStart, rangeEnd }] } }.
export interface TimelineRange {
  name: string;
  rangeStart: string;
  rangeEnd: string;
}

export interface TimelinePayload {
  langs: Record<string, TimelineRange[]>;
}

export interface StatusBarPayload {
  data: {
    grand_total: { text: string };
    categories: unknown[];
  };
}

export interface ProjectListPayload {
  projects: string[];
}

// ---------------------------------------------------------------------------
// Spaces — — named, rule-based scopes (a Space per context: work/personal/…).
// ---------------------------------------------------------------------------

// How a Space rule's matchValue is interpreted. Membership is exact|regex only
// (template is a transform, meaningless for membership).
export type SpaceMatchType = "exact" | "regex";

export interface SpaceRule {
  id: number;
  axis: string;
  matchValue: string;
  matchType: SpaceMatchType;
}

// GET /spaces — the list rows.
export interface Space {
  id: number;
  name: string;
  position: number;
  ruleCount: number;
}

// GET /spaces/:id — a single Space with its membership rules.
export interface SpaceDetail {
  id: number;
  name: string;
  position: number;
  rules: SpaceRule[];
}

export interface AddSpaceRuleBody {
  axis: string;
  matchValue: string;
  matchType: SpaceMatchType;
}

// GET /spaces/preview — the raw values an (unsaved) rule currently matches.
export interface SpacePreviewValue {
  value: string;
  count: number;
}

export interface SpacePreview {
  values: SpacePreviewValue[];
  truncated: boolean;
}

// ---------------------------------------------------------------------------
// Leaderboards
// ---------------------------------------------------------------------------

export interface LeaderboardEntry {
  name: string;
  value: number;
}

// Normalized shape returned by api.getLeaderboards(). Backend emits hakatime's
// raw { global, lang }; api.ts maps the per-language key `lang` -> `languages`.
export interface LeaderboardsPayload {
  global: LeaderboardEntry[];
  languages: Record<string, LeaderboardEntry[]>;
}

// ---------------------------------------------------------------------------
// Badges / Import / Commits
// ---------------------------------------------------------------------------

export interface BadgeLinkPayload {
  badgeUrl: string;
}

export interface ImportRequest {
  // base64-encoded before sending. Omitted entirely when the user leaves the
  // token blank so the server falls back to its env-configured key.
  apiToken?: string;
  startDate: string; // ISO
  endDate: string; // ISO
}

// GET /import/config -> whether the server has a Wakatime key configured.
export interface ImportConfigPayload {
  hasServerKey: boolean; // TODO verify key
}

// POST /import/wakatime-range -> how far back the user's wakatime.com data goes.
export interface WakatimeRangePayload {
  startDate: string; // YYYY-MM-DD
  endDate: string; // YYYY-MM-DD
  totalSeconds: number;
  text: string; // human-readable duration, e.g. "3 mos 4 days"
  hasData: boolean;
}

export type JobStatus =
  | "JobSubmitted"
  | "JobPending"
  | "JobFailed"
  | "JobFinished"
  | string;

export interface ImportStatusPayload {
  jobStatus: JobStatus;
}

// --- First-class import jobs (durable, streamed over WebSocket) --------------

export type ImportJobState =
  | "queued"
  | "running"
  | "completed"
  | "failed"
  | "cancelled";

export interface ImportJob {
  id: number;
  owner: string;
  state: ImportJobState;
  startDate: string;
  endDate: string;
  totalDays: number;
  processedDays: number;
  importedCount: number;
  currentDay: string | null;
  error: string | null;
  createdAt: string;
  startedAt: string | null;
  finishedAt: string | null;
}

export interface ImportLogLine {
  id: number;
  ts: string;
  level: string;
  message: string;
}

// --- Data curation (non-destructive hides + persistent rename rules) ---------

export type CurationAction = "hide" | "rename";

// How a rename rule's matchValue is interpreted. Hide rules are always exact.
// How a rename rule's matchValue is interpreted:
//  - exact:    literal value == matchValue
//  - regex:    matchValue is a regex; matching values map to newValue
//  - template: matchValue is a regex, newValue is a regexp_replace template with
//              `\1` backrefs (e.g. `^@(.*)$` + `\1` strips a leading `@`).
export type CurationMatchType = "exact" | "regex" | "template";

export interface CurationRule {
  id: number;
  axis: string;
  action: CurationAction;
  matchValue: string;
  newValue: string | null;
  // Defaults to "exact" when the backend omits it (older rules / hide rules).
  matchType?: CurationMatchType;
  createdAt: string;
}

export interface CurationRulesPayload {
  rules: CurationRule[];
}

export interface AddCurationRuleBody {
  axis: string;
  action: CurationAction;
  matchValue: string;
  newValue?: string;
  matchType?: CurationMatchType;
}

export interface AddCurationRulePayload {
  rule: CurationRule;
}

// GET /api/v1/users/current/curation/:id/affected — the raw values a rule
// currently matches, with their heartbeat counts and (for regex/template rules)
// the value they map to in the dashboards.
export interface CurationAffectedValue {
  value: string;
  count: number;
  // The mapped-to value for this raw value (exact/regex: the rule's newValue;
  // template: regexp_replace applied). Optional until the backend emits it.
  mappedTo?: string;
}

export interface CurationAffectedPayload {
  values: CurationAffectedValue[];
  truncated?: boolean;
}

// --- Heartbeats Explorer -----------------------------------------------------

// Whitelisted axes to group/filter heartbeats by. `day` is a "YYYY-MM-DD"
// bucket; the rest map directly to HeartbeatRow columns.
export type HeartbeatAxis =
  | "day"
  | "project"
  | "language"
  | "editor"
  | "plugin"
  | "platform"
  | "machine"
  | "branch"
  | "category"
  | "type"
  | "entity"
  | "isWrite"
  | "userAgent";

// Accumulated group filters keyed by axis. For `day` the value is "YYYY-MM-DD".
export type HeartbeatFilters = Partial<Record<HeartbeatAxis, string>>;

export interface HeartbeatRow {
  id: number;
  time: string;
  entity: string;
  type: string;
  project: string | null;
  language: string | null;
  editor: string | null;
  plugin: string | null;
  platform: string | null;
  machine: string | null;
  branch: string | null;
  category: string | null;
  isWrite: boolean | null;
  lineno: number | null;
  cursorpos: string | null;
  fileLines: number | null;
  dependencies: string[] | null;
  userAgent: string | null;
}

export interface HeartbeatGroup {
  value: string | null;
  count: number;
  // Attributed coding time for the group (gap-coalesced within timeLimit).
  seconds: number;
  firstSeen: string;
  lastSeen: string;
}

export interface HeartbeatGroupPayload {
  groupBy: HeartbeatAxis;
  groups: HeartbeatGroup[];
  // The group query is capped server-side; true when more groups were dropped.
  truncated?: boolean;
}

export interface HeartbeatListPayload {
  items: HeartbeatRow[];
  total: number;
  page: number;
  limit: number;
}

// GET /api/v1/users/current/heartbeats/latest -> most-recent heartbeat marker.
export interface LatestHeartbeatPayload {
  lastHeartbeat: string | null; // ISO timestamp of the newest heartbeat
  count: number;
}

export interface SubmitImportResponse {
  jobId: number;
  jobStatus: string;
}

export interface ImportJobsListPayload {
  jobs: ImportJob[];
}

export interface ImportJobDetailPayload {
  job: ImportJob;
  logs: ImportLogLine[];
}

export interface CancelImportPayload {
  job: ImportJob;
}

// WebSocket messages the server pushes to the client.
export type ImportSocketMessage =
  | { type: "snapshot"; job: ImportJob; logs: ImportLogLine[] }
  | { type: "log"; log: ImportLogLine }
  | { type: "progress"; job: ImportJob }
  | { type: "state"; job: ImportJob };

export function isTerminalState(state: ImportJobState): boolean {
  return (
    state === "completed" || state === "failed" || state === "cancelled"
  );
}

export interface Commit {
  html_url: string;
  total_seconds: number;
  commit: {
    message: string;
    author: { date: string };
  };
  author: { login: string };
}

export interface CommitReportPayload {
  commits: Commit[];
}

// ---------------------------------------------------------------------------
// Common query param shapes
// ---------------------------------------------------------------------------

export interface StatsParams {
  start: string;
  end: string;
  timeLimit?: number;
  // When set, scopes the dashboard to a Space's members.
  space?: string | number;
}

export interface RangeParams {
  start: string;
  end: string;
  // When set, scopes the results to a Space's members.
  space?: string | number;
}

// GET /api/v1/users/current/files — top files across ALL projects. `projects`
// is the number of distinct projects the file touches; files with projects > 1
// are cross-project "lynchpins" (shared interfaces / comm channels). Ordered
// lynchpins-first (projects desc, then time desc).
export interface CrossProjectFile {
  entity: string;
  seconds: number;
  projects: number;
}

export interface CrossProjectFilesPayload {
  files: CrossProjectFile[];
  truncated?: boolean;
}
