import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";

/**
 * Per-source ingestion health (editor/plugin/machine last check-in). Powers the
 * Heartbeats "Source health" panel — the "is my wakatime plugin still
 * reporting" view. Refetches periodically so a plugin going silent surfaces
 * without a manual reload.
 */
export function useSourceHealth() {
  return useQuery({
    queryKey: ["sources-health"],
    queryFn: api.getSourceHealth,
    refetchInterval: 60_000,
  });
}
