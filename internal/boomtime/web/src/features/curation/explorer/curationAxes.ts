import { CURATABLE_AXES } from "@boomtime/features/rules/axes";
import type { HeartbeatAxis } from "@/types/api";

// Axes whose curation "hide" rules the backend actually excludes from the
// dashboards. The curation Suppress toggle is only offered for these — a hide
// rule on any other axis would be a no-op against the dashboards.
//
// Backend coverage (LoadHiddenSets / exclusionPredicate) spans all of these:
// every aggregate dashboard (raw + rollup stats, projects list, leaderboards,
// category/punchcard/sessions/momentum) excludes a suppressed value; the rollup
// falls back to a raw gap_seconds scan for plugin/branch/category. Verified by
// internal/db/suppression_test.go (TestSuppressedValuesExcludedFromAggregations).
// `day`, `type`, `entity`, and `userAgent` are never suppressible.
export const SUPPRESSIBLE_AXES: ReadonlySet<HeartbeatAxis> =
  new Set(CURATABLE_AXES);

export function isSuppressibleAxis(axis: string): boolean {
  return SUPPRESSIBLE_AXES.has(axis as HeartbeatAxis);
}
