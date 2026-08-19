import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import type { ComboboxOption } from "@/components/ui/combobox";
import type { HeartbeatAxis } from "@/types/api";

// Wide, all-time window so the distinct-value list is complete regardless of
// the dashboard's current date range.
const ALL_TIME_START = "2000-01-01T00:00:00.000Z";

/**
 * Distinct real values for a heartbeat axis (project/editor/plugin/machine/...),
 * with counts, sorted by count desc — for populating autocomplete pickers.
 * Backed by the existing group endpoint, so no backend change is needed.
 */
export function useAxisValues(
  axis: HeartbeatAxis | null,
  enabled = true,
): { options: ComboboxOption[]; isLoading: boolean } {
  const query = useQuery({
    queryKey: qk.axisValues(axis),
    enabled: enabled && axis !== null,
    // Distinct values change slowly; cache generously.
    staleTime: 5 * 60_000,
    queryFn: () =>
      api.groupHeartbeats({
        groupBy: axis as HeartbeatAxis,
        start: ALL_TIME_START,
        end: new Date().toISOString(),
      }),
  });

  const options: ComboboxOption[] = (query.data?.groups ?? [])
    // Skip null buckets — they aren't selectable label values.
    .filter((g) => g.value != null && g.value !== "")
    .map((g) => ({ value: g.value as string, count: g.count }))
    .sort((a, b) => (b.count ?? 0) - (a.count ?? 0));

  return { options, isLoading: query.isLoading };
}
