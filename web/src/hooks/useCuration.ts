import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { AddCurationRuleBody } from "@/types/api";

// Query keys whose results depend on curation rules (hides filter dashboards;
// renames rewrite heartbeat values). Invalidated after any rule change.
const DEPENDENT_KEYS = [
  ["stats"],
  ["project-stats"],
  ["projects"],
  ["leaderboards"],
  ["heartbeats-group"],
  ["heartbeats-list"],
  ["derived-status"],
];

export function useCurationRules() {
  return useQuery({
    queryKey: ["curation"],
    queryFn: () => api.getCurationRules(),
  });
}

export function useCurationMutations() {
  const qc = useQueryClient();

  function invalidateDependents() {
    qc.invalidateQueries({ queryKey: ["curation"] });
    for (const key of DEPENDENT_KEYS) {
      qc.invalidateQueries({ queryKey: key });
    }
  }

  const add = useMutation({
    mutationFn: (body: AddCurationRuleBody) => api.addCurationRule(body),
    onSuccess: invalidateDependents,
  });

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteCurationRule(id),
    onSuccess: invalidateDependents,
  });

  return { add, remove };
}
