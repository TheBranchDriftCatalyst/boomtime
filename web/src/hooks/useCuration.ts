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
  // The new TanStack-table explorer's own query keys.
  ["hb-explore-group"],
  ["hb-explore-list"],
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

  // Edit an existing rename rule. Rule identity is
  // (axis, action, matchType, matchValue); CreateCurationRule UPSERTs newValue
  // on that key. So:
  //  - If the identity is UNCHANGED (only newValue differs), a plain create is
  //    an upsert that updates the target.
  //  - If the pattern or matchType CHANGED (new identity), delete the old rule
  //    and create the new one so no stale rule lingers. Non-destructive: raw
  //    heartbeat records are never touched.
  const edit = useMutation({
    mutationFn: async ({
      oldId,
      identityChanged,
      body,
    }: {
      oldId: number;
      identityChanged: boolean;
      body: AddCurationRuleBody;
    }) => {
      if (identityChanged) {
        await api.deleteCurationRule(oldId);
      }
      return api.addCurationRule(body);
    },
    onSuccess: invalidateDependents,
  });

  return { add, remove, edit };
}
