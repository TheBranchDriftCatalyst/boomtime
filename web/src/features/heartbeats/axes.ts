import { CURATABLE_AXES } from "@/features/rules/axes";
import type { HeartbeatAxis } from "@/types/api";

// Explorer-only constants. The shared axis metadata (AXES, axisLabel) lives in
// @/lib/axes since curation, rules, and spaces consume it too.

export const DEFAULT_GROUP_BY: HeartbeatAxis[] = ["project", "day"];

export const LEAF_PAGE_SIZE = 50;

// Axes whose curation "hide" rules the backend actually excludes from the
// dashboards. The Explorer's Suppress toggle is only offered for these — a hide
// rule on any other axis would be a no-op against the dashboards.
//
// Backend coverage (LoadHiddenSets / exclusionPredicate) spans all 8 of these:
// every aggregate dashboard (raw + rollup stats, projects list, leaderboards,
// category/punchcard/sessions/momentum) excludes a suppressed value; the rollup
// falls back to a raw gap_seconds scan for plugin/branch/category. Verified by
// internal/db/suppression_test.go (TestSuppressedValuesExcludedFromAggregations).
// `day`, `type`, `entity`, and `userAgent` are never suppressible.
export const SUPPRESSIBLE_AXES: ReadonlySet<HeartbeatAxis> =
  new Set(CURATABLE_AXES);

export function isSuppressibleAxis(axis: HeartbeatAxis): boolean {
  return SUPPRESSIBLE_AXES.has(axis);
}
