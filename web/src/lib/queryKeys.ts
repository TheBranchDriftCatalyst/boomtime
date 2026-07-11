// Central query-key factory. Every React Query cache key in the app is built
// here so key shapes stay consistent across pages/hooks and invalidation
// prefixes can't drift from the keys they target.
//
// IMPORTANT: element order and arity are cache behavior. Keys must keep the
// exact shape the call sites used before centralization; change a shape only
// on purpose (it busts/forks the cache for that domain).
import type {
  HeartbeatAxis,
  HeartbeatFilters,
  SpaceMatchType,
} from "@/types/api";

type SpaceScope = string | number | undefined;

// Invalidation prefixes: match every key in a domain regardless of the
// range/filter tail (TanStack matches keys by prefix).
const prefix = {
  stats: ["stats"] as const,
  projectStats: ["project-stats"] as const,
  projects: ["projects"] as const,
  leaderboards: ["leaderboards"] as const,
  timeline: ["timeline"] as const,
  punchcard: ["punchcard"] as const,
  sessions: ["sessions"] as const,
  momentum: ["momentum"] as const,
  crossProjectFiles: ["cross-project-files"] as const,
  hbExploreGroup: ["hb-explore-group"] as const,
  hbExploreList: ["hb-explore-list"] as const,
  derivedStatus: ["derived-status"] as const,
  axisValues: ["axis-values"] as const,
  curationAffected: ["curation-affected"] as const,
};

// Dashboard keys whose results are scoped by a Space (?space=…) and rewritten
// by curation renames/hides. Any Space or curation rule change invalidates
// these so every open dashboard refetches.
const dashboardDependents = [
  prefix.stats,
  prefix.projectStats,
  prefix.projects,
  prefix.leaderboards,
  prefix.timeline,
  prefix.punchcard,
  prefix.sessions,
  prefix.momentum,
  prefix.crossProjectFiles,
] as const;

// Curation rule changes additionally reshape the heartbeats explorer, the
// derived-data health, and the distinct values per axis.
const curationDependents = [
  ...dashboardDependents,
  prefix.hbExploreGroup,
  prefix.hbExploreList,
  prefix.derivedStatus,
  prefix.axisValues,
] as const;

export const qk = {
  prefix,
  dashboardDependents,
  curationDependents,

  // --- Auth / tokens ---------------------------------------------------------
  tokens: () => ["tokens"] as const,

  // --- Stats / dashboards ----------------------------------------------------
  // The canonical 5-element stats key ALWAYS includes the space slot (undefined
  // when unscoped) so Overview, SpaceView, and Projects share one cache entry
  // for the same range.
  stats: (start: string, end: string, timeLimit?: number, space?: SpaceScope) =>
    ["stats", start, end, timeLimit, space] as const,
  timeline: (hours: number, timeLimit?: number, space?: SpaceScope) =>
    ["timeline", hours, timeLimit, space] as const,
  punchcard: (
    start: string,
    end: string,
    timeLimit?: number,
    space?: SpaceScope,
  ) => ["punchcard", start, end, timeLimit, space] as const,
  sessions: (
    start: string,
    end: string,
    timeLimit?: number,
    space?: SpaceScope,
  ) => ["sessions", start, end, timeLimit, space] as const,
  momentum: (start: string, end: string, space?: SpaceScope) =>
    ["momentum", start, end, space] as const,
  leaderboards: (start: string, end: string) =>
    ["leaderboards", start, end] as const,

  // --- Projects ----------------------------------------------------------------
  projects: (start: string, end: string) => ["projects", start, end] as const,
  projectStats: (
    project: string | null,
    start: string,
    end: string,
    timeLimit?: number,
  ) => ["project-stats", project, start, end, timeLimit] as const,
  crossProjectFiles: (start: string, end: string, timeLimit?: number) =>
    ["cross-project-files", start, end, timeLimit] as const,

  // --- Spaces ------------------------------------------------------------------
  spaces: () => ["spaces"] as const,
  space: (id: number | string | null | undefined) =>
    ["space", id != null ? String(id) : null] as const,
  spacePreview: (axis: string, matchType: SpaceMatchType, matchValue: string) =>
    ["space-preview", axis, matchType, matchValue] as const,

  // --- Curation ----------------------------------------------------------------
  curation: () => ["curation"] as const,
  curationAffected: (id: number) => ["curation-affected", id] as const,

  // --- Heartbeats explorer / health ---------------------------------------------
  axisValues: (axis: HeartbeatAxis | null) => ["axis-values", axis] as const,
  hbExploreGroup: (
    axis: HeartbeatAxis,
    filters: HeartbeatFilters,
    start: string,
    end: string,
    timeLimit: number,
  ) => ["hb-explore-group", axis, filters, start, end, timeLimit] as const,
  hbExploreList: (
    filters: HeartbeatFilters,
    entity: string,
    start: string,
    end: string,
    page: number,
  ) => ["hb-explore-list", filters, entity, start, end, page] as const,
  derivedStatus: () => ["derived-status"] as const,
  sourcesHealth: () => ["sources-health"] as const,
  latestHeartbeat: () => ["latest-heartbeat"] as const,

  // --- Import ------------------------------------------------------------------
  importJobs: () => ["import-jobs"] as const,
  importJob: (id: number) => ["import-job", id] as const,
  importConfig: () => ["import-config"] as const,

  // --- Meta (version + changelog) ---------------------------------------------
  // Both cache forever — the FE only refetches on a manual reload; a new
  // release replaces the whole SPA anyway.
  version: () => ["meta", "version"] as const,
  changelog: () => ["meta", "changelog"] as const,
};
