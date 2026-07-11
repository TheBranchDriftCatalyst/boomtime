// Stats / projects / timeline types mirroring the Go backend JSON payloads.
// JSON key naming follows hakatime's noPrefixOptions (drop the lowercase type
// prefix, e.g. pName -> name). Where a key is uncertain it is flagged with a
// `// TODO verify key` comment so it can be reconciled with the backend.

export interface ResourceStats {
  name: string;
  totalSeconds: number;
  totalPct: number;
  totalDaily: number[];
  pctDaily: number[];
  // gaka-7m4: populated only on the synthesized "Other (N more)" entry —
  // top otherMembersCap tail members so tooltips can break down what "Other"
  // contains. Non-Other rows serialize without these keys.
  otherMembers?: OtherMember[];
  otherCount?: number;
}

// One tail entry carried on a synthesized "Other" ResourceStats. Range-total
// only — no per-day arrays (would defeat the capWithOther payload cap).
export interface OtherMember {
  name: string;
  totalSeconds: number;
  totalPct: number;
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
interface PunchcardCell {
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
interface SessionsSummary {
  count: number;
  totalSeconds: number;
  avgSeconds: number;
  maxSeconds: number;
  medianSeconds: number;
}
interface SessionsDaily {
  date: string; // YYYY-MM-DD
  sessions: number;
  totalSeconds: number;
  longestSeconds: number;
}
interface SessionsHistogramBin {
  label: string;
  count: number;
}
export interface SessionsPayload {
  summary: SessionsSummary;
  daily: SessionsDaily[];
  histogram: SessionsHistogramBin[];
}

// GET stats/momentum?top=N -> project x week activity grid.
interface MomentumProject {
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
  // Per-day-per-language matrix for the SAME top-N (+ "Other (N more)") set as
  // `languages`, each `daily` aligned index-for-index to `dailyTotal`. Summing
  // across series for a day equals dailyTotal[day]. Powers the language-stacked
  // "Total activity" column. Optional so the UI degrades until the backend lands.
  languagesDaily?: { name: string; daily: number[] }[];
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

export interface ProjectListPayload {
  projects: string[];
}

// GET /badge/link/:project -> the shields.io badge URL for a project.
export interface BadgeLinkPayload {
  badgeUrl: string;
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

// ---------------------------------------------------------------------------
// Common query param shapes
// ---------------------------------------------------------------------------

// NOTE: these are `type` aliases (not `interface`s) on purpose: object-literal
// type aliases get an implicit index signature, which keeps them assignable to
// the api client's `Params` (Record<string, ...>) without widening the shape.
export type StatsParams = {
  start: string;
  end: string;
  timeLimit?: number;
  // When set, scopes the dashboard to a Space's members.
  space?: string | number;
};

export type RangeParams = {
  start: string;
  end: string;
  // When set, scopes the results to a Space's members.
  space?: string | number;
};
