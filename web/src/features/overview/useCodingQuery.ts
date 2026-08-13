// useCodingQuery — a thin react-query wrapper over the cross-domain query DSL
// client (`runQuery`, @/lib/queryApi), the coding-domain sibling of the reading
// dashboard's `useReadingQuery`. Every coding tile migrated onto the DSL is one
// declarative QuerySpec; this hook binds a spec to react-query so the tile gets
// the standard loading / error / cached-refetch ladder for free (and dedupes
// when two tiles ask for the same spec).
//
// WHY the queryKey is nested under ["curation", …] rather than a top-level
// ["coding-query"] prefix (as reading uses ["reading-query"]):
//
//   The coding projects breakdown is CURATION-DEPENDENT — the backend
//   transparently reshapes it from the caller's canonical pins (curation rules
//   with action="pin"; see internal/queryapi.applyCanonicalPins). The shared
//   pin control (`features/pins/usePins`) invalidates the ["curation"] prefix
//   on every pin/unpin, and react-query prefix-matches, so keying this query as
//   a CHILD of ["curation"] makes a pin/unpin refetch it automatically — the
//   pinned project then visibly escapes "Other". This mirrors the very idiom
//   usePins itself uses for the pins list (["curation", "pins"]), and needs NO
//   edit to the reused, shared pins module. (A dedicated ["coding-query"]
//   prefix would be marginally tidier but would require teaching usePins about
//   it; nesting under curation is the FE-only path and is honest — this view IS
//   a projection of curation state.)
import { useQuery } from "@tanstack/react-query";
import { runQuery, type QueryResult, type QuerySpec } from "@/lib/queryApi";

export function useCodingQuery(spec: QuerySpec, enabled = true) {
  return useQuery<QueryResult>({
    queryKey: ["curation", "coding-query", spec],
    queryFn: () => runQuery(spec),
    enabled,
    // Coding rollups move slowly within a session (a heartbeat sync writes new
    // rows a few times a day); a 60s stale window keeps tab-flips instant.
    staleTime: 60_000,
  });
}
