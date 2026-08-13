// useReadingAxisValues — distinct-value autocomplete for a reading goal's
// dimension (genre / series / status), mirroring useAxisValues on the coding
// path (gaka-wpb). Where the coding path groups heartbeats over
// /api/v1/heartbeats/group, the reading path has no such endpoint — it goes
// through the cross-domain query DSL (`runQuery`, POST /api/v1/query), grouping
// the reading domain's "books" measure by the axis.
//
// Why "books" + topN 500 + other:false:
//   - measure "books" counts finished books per group key; grouping by the axis
//     yields one GroupRow per distinct value (the `key`).
//   - topN 500 pulls the full practical distinct set (a reader's genre/series
//     list is small; 500 is generous and matches the datalist render cap the
//     coding path uses).
//   - other:false suppresses the synthetic "Other (N more)" rollup bucket, so
//     the list is real distinct values only. We ALSO filter that sentinel out
//     defensively with the same regex AxisValueInput uses, in case a future
//     query path routes through a capped source.
//
// Feature-gate handling: when the books/reading domain is disabled server-side,
// runQuery returns 404. We swallow that specific case and return an empty list
// (no suggestions) rather than throwing — the value input still works, just
// without autocomplete. Any OTHER error propagates so it isn't masked.
//
// Non-existing values are STILL accepted downstream — the datalist is
// suggest-only, so aspirational reading goals ("finish the Foundation series
// before I own any of it") work by just typing.
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "@/lib/api";
import { runQuery } from "@/lib/queryApi";
import type { GoalReadingAxis } from "@/types/api";

// Synthetic rollup bucket the query DSL emits for the long tail — never a real
// user value. Same shape AxisValueInput filters on the coding path.
const OTHER_SENTINEL = /^Other(\s*\(\d+\s*more\))?$/;

export function useReadingAxisValues(
  axis: GoalReadingAxis | null,
): { options: string[]; isLoading: boolean } {
  const query = useQuery({
    // Axis is part of the key so each dimension caches independently.
    queryKey: ["reading-axis-values", axis],
    enabled: axis !== null,
    // Distinct values change slowly; cache generously (matches useAxisValues).
    staleTime: 5 * 60_000,
    queryFn: async (): Promise<string[]> => {
      try {
        const result = await runQuery({
          domain: "reading",
          measure: "books",
          group: axis as GoalReadingAxis,
          bucket: { topN: 500, other: false },
        });
        if (result.kind !== "groups") return [];
        return result.groups.map((g) => g.key);
      } catch (err) {
        // Reading/books feature gated off → 404. Degrade to no suggestions
        // instead of surfacing an error; the value input keeps working.
        if (err instanceof ApiError && err.status === 404) return [];
        throw err;
      }
    },
  });

  const options: string[] = (query.data ?? [])
    .filter((k) => typeof k === "string" && k.length > 0)
    .filter((k) => !OTHER_SENTINEL.test(k))
    .slice(0, 500); // browsers cap datalist rendering; 500 is generous

  return { options, isLoading: query.isLoading };
}
