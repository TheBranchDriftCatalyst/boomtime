// CodingProjectsBreakdown — the Overview "Project breakdown" tile, migrated off
// the old stats endpoint (`api.getStats().projects` → PieChart) onto the
// cross-domain query DSL (gaka-canon). It issues ONE coding spec —
//
//   from(coding)·group(project)·measure(seconds)·bucket(topN + Other)
//
// bound to the Overview's active date range — and renders the grouped seconds
// through CodingProjectsDonut. Because the query rides the DSL, the backend
// transparently honors the caller's canonical PINS on the "project" axis
// (internal/queryapi.applyCanonicalPins): a pinned project always keeps its own
// slice even at low share, top-N others follow by share, and the tail rolls
// into "Other". The donut's per-project pin toggles (pinAxis="project") let the
// user canonize a project inline; usePins invalidates the ["curation"] prefix
// this query is keyed under, so pinning refetches the tile and the project
// escapes Other.
//
// Scope note: the DSL is inherently per-caller (owner is resolved from auth;
// QuerySpec carries no `space`), so this tile is used only for the UNSCOPED
// global Overview. The Space-scoped OverviewDashboard keeps the legacy
// space-aware PieChart — see OverviewDashboard.
import { useMemo } from "react";
import { Spinner } from "@thebranchdriftcatalyst/catalyst-ui/ui/spinner";
import type { QueryResult, QuerySpec } from "@shared/lib/queryApi";
import { useCodingQuery } from "./useCodingQuery";
import { CodingProjectsDonut } from "./CodingProjectsDonut";

// Top-N kept as their own slices before the tail collapses into "Other". Pinned
// projects are ADDITIVE to this (they always survive), matching the reading
// genre donut's topN=6 + other policy.
const TOP_N = 8;

interface CodingProjectsBreakdownProps {
  /** Active Overview window start (RFC3339). */
  startISO: string;
  /** Active Overview window end (RFC3339). */
  endISO: string;
  height?: number;
}

const asGroups = (r?: QueryResult) => (r?.kind === "groups" ? r.groups : []);

export function CodingProjectsBreakdown({
  startISO,
  endISO,
  height = 320,
}: CodingProjectsBreakdownProps) {
  // Spec is memoized on the range so react-query's deep-hashed key is stable
  // across re-renders (a changed range refetches; an identical one dedupes).
  const spec = useMemo<QuerySpec>(
    () => ({
      domain: "coding",
      measure: "seconds",
      group: "project",
      over: {
        granularity: "none",
        range: { between: { start: startISO, end: endISO } },
      },
      bucket: { topN: TOP_N, other: true },
    }),
    [startISO, endISO],
  );

  const q = useCodingQuery(spec);

  if (q.isLoading) {
    return (
      <div
        className="flex items-center justify-center text-sm text-muted-foreground"
        style={{ height }}
      >
        <Spinner />
      </div>
    );
  }
  if (q.isError) {
    return (
      <div
        className="flex items-center justify-center text-sm text-muted-foreground"
        style={{ height }}
      >
        Failed to load project breakdown.
      </div>
    );
  }

  return (
    <CodingProjectsDonut
      data={asGroups(q.data)}
      height={height}
      emptyHint="No project activity in this range."
      pinAxis="project"
    />
  );
}

export default CodingProjectsBreakdown;
