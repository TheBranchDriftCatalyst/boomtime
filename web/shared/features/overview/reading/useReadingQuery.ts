// useReadingQuery — a thin react-query wrapper over the cross-domain query DSL
// client (`runQuery`, @shared/lib/queryApi). Every Reading dashboard tile is one
// declarative QuerySpec; this hook binds a spec to react-query so the tiles get
// the standard loading / error / cached-refetch ladder for free (and dedupe
// when two tiles ask for the same spec).
//
// The queryKey is `["reading-query", spec]` — react-query deep-hashes the spec
// object, so two structurally-equal specs share a cache entry and a changed
// spec refetches. `enabled` lets a tile defer its fetch (e.g. the whole
// dashboard is gated on books_enabled upstream, but a tile may also self-gate).
import { useQuery } from "@tanstack/react-query";
import { runQuery, type QueryResult, type QuerySpec } from "@shared/lib/queryApi";

export function useReadingQuery(spec: QuerySpec, enabled = true) {
  return useQuery<QueryResult>({
    queryKey: ["reading-query", spec],
    queryFn: () => runQuery(spec),
    enabled,
    // The reading aggregates move slowly (a sync writes new rows at most a few
    // times a day); a 60s stale window keeps tab-flips instant without going
    // stale for a session.
    staleTime: 60_000,
  });
}
