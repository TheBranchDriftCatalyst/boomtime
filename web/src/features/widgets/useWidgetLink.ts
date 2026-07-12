import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import type { WidgetScope } from "@/types/api";

// Lazily mint/fetch the widget link for a scope. `enabled` gates the fetch so
// panels/hover-actions only mint when actually opened/clicked — minting is an
// upsert, so re-fetches always return the same stable uuid.
export function useWidgetLink(
  scopeType: WidgetScope,
  scopeRef = "",
  enabled = true,
) {
  return useQuery({
    queryKey: qk.widgetLink(scopeType, scopeRef),
    queryFn: () => api.getWidgetLink(scopeType, scopeRef),
    enabled,
    staleTime: Infinity, // the uuid never changes for a scope
  });
}
