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
  aiActivity: ["ai-activity"] as const,
  healthActivity: ["health-activity"] as const,
  workoutList: ["workout-list"] as const,
  crossProjectFiles: ["cross-project-files"] as const,
  hbExploreGroup: ["hb-explore-group"] as const,
  hbExploreList: ["hb-explore-list"] as const,
  derivedStatus: ["derived-status"] as const,
  axisValues: ["axis-values"] as const,
  curationAffected: ["curation-affected"] as const,
  // gaka-cr4 + gaka-due: shared preview key for /curation/:id/preview.
  // ONE key covers both variants (apply for rename, purge for hide) — the
  // backend dispatches on rule.action and the FE modal renders accordingly.
  curationActionPreview: ["curation-action-preview"] as const,
  entitiesByType: ["entities-by-type"] as const,
  // gaka-wpb: goals list + per-goal + batched progress.
  goals: ["goals"] as const,
  goalProgress: ["goal-progress"] as const,
  goalsProgress: ["goals-progress"] as const,
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
  aiActivity: (start: string, end: string) =>
    ["ai-activity", start, end] as const,
  healthActivity: (start: string, end: string) =>
    ["health-activity", start, end] as const,
  workoutList: (start: string, end: string) =>
    ["workout-list", start, end] as const,
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
  // gaka-cr4 + gaka-due: destructive-action preview key (fetched once per
  // modal open). Same key for both apply (rename) and purge (hide) variants
  // — the backend dispatches on rule.action; ONE cache entry per rule id.
  curationActionPreview: (id: number) =>
    ["curation-action-preview", id] as const,

  // --- Heartbeats explorer / health ---------------------------------------------
  axisValues: (axis: HeartbeatAxis | null) => ["axis-values", axis] as const,
  hbExploreGroup: (
    axis: HeartbeatAxis,
    filters: HeartbeatFilters,
    start: string,
    end: string,
    timeLimit: number,
    entity: string,
  ) =>
    ["hb-explore-group", axis, filters, start, end, timeLimit, entity] as const,
  hbExploreList: (
    filters: HeartbeatFilters,
    entity: string,
    start: string,
    end: string,
    page: number,
  ) => ["hb-explore-list", filters, entity, start, end, page] as const,
  derivedStatus: () => ["derived-status"] as const,
  entitiesByType: (ty: string) => ["entities-by-type", ty] as const,
  sourcesHealth: () => ["sources-health"] as const,
  latestHeartbeat: () => ["latest-heartbeat"] as const,

  // --- Import ------------------------------------------------------------------
  importJobs: () => ["import-jobs"] as const,
  importJob: (id: number) => ["import-job", id] as const,
  importConfig: () => ["import-config"] as const,
  // Per-user encrypted Wakatime key presence (gaka-6jm.2). Value is
  // {hasSavedKey}; invalidated after save/delete so UI affordances update.
  wakatimeKey: () => ["wakatime-key"] as const,
  // Per-user IANA timezone (gaka-dg7). Value is {timezone, effectiveTimezone};
  // invalidated after PATCH so the Settings picker + any downstream FE
  // decision (e.g. the "Using X (from server default)" hint) re-renders.
  timezone: () => ["timezone"] as const,
  // Public profile (gaka-6jm.1): the caller's enable-toggle + slug. Used by
  // the Settings card AND the Sidebar (to conditionally show the "Public
  // profile" nav link when enabled). Invalidated after PUT so both consumers
  // re-fetch atomically.
  publicProfile: () => ["public-profile"] as const,
  publicDashboard: (slug: string) => ["public-dashboard", slug] as const,
  // gaka-keb: per-user, per-scope dashboard layout. Scope key held loose
  // (string) so future scopes ("overview", "space:12") land without a
  // signature widening.
  dashboardLayout: (scope: string) => ["dashboard-layout", scope] as const,

  // gaka-myv: admin label-images status (row count / feature flags).
  // Refetched after a regenerate to update the "N / M generated" tally.
  adminLabelImages: () => ["admin", "label-images"] as const,

  // gaka-364.3: DB-backed labels catalog. Public (no owner scoping) —
  // one key shared by every consumer (evaluator, hero widget, admin
  // table). Invalidated after PATCH/POST/DELETE on /admin/labels.
  labelsCatalog: () => ["labels", "catalog"] as const,

  // gaka-vh8: git-history backfill config + stats. Both are per-user
  // and admin-only; separate keys because a config save and a batch
  // POST invalidate different things.
  backfillConfig: () => ["admin", "backfill", "config"] as const,
  backfillStats: () => ["admin", "backfill", "stats"] as const,

  // gaka-9v4: per-user chibi avatar status (polled while a render is
  // in flight). Public-image consumers don't need a query key — the
  // <img src> URL is stable + cache-busted with the generatedAt hint.
  avatarStatus: () => ["user-avatar", "status"] as const,

  // --- Meta (version + changelog) ---------------------------------------------
  // Both cache forever — the FE only refetches on a manual reload; a new
  // release replaces the whole SPA anyway.
  version: () => ["meta", "version"] as const,
  changelog: () => ["meta", "changelog"] as const,

  // --- Embeddable widgets --------------------------------------------------------
  widgetLink: (scopeType: string, scopeRef: string) =>
    ["widget-link", scopeType, scopeRef] as const,
  widgetLinks: () => ["widget-links"] as const,

  // --- Goals (gaka-wpb) --------------------------------------------------------
  // Full list (fetch once, hydrate the Settings > Goals table). Per-id
  // isn't a separate key — the FE reads the list to render, and the
  // list already carries the row shape.
  goals: () => ["goals"] as const,
  // Per-goal progress. Composable widgets use qk.goalProgress(id) so
  // one tile invalidates in isolation; the batched dashboard call
  // uses qk.goalsProgress().
  goalProgress: (id: string) => ["goal-progress", id] as const,
  goalsProgress: () => ["goals-progress"] as const,
};
