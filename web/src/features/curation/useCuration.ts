import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import type { AddCurationRuleBody } from "@/types/api";

export function useCurationRules() {
  return useQuery({
    queryKey: qk.curation(),
    queryFn: () => api.getCurationRules(),
  });
}

export function useCurationMutations() {
  const qc = useQueryClient();

  // Query keys whose results depend on curation rules (hides filter
  // dashboards; renames rewrite heartbeat values). The backend applies renames
  // to ALL aggregations, so every dashboard query key plus the explorer /
  // derived-status / per-axis value keys is invalidated after any rule change.
  function invalidateDependents() {
    qc.invalidateQueries({ queryKey: qk.curation() });
    for (const key of qk.curationDependents) {
      qc.invalidateQueries({ queryKey: key });
    }
  }

  const add = useMutation({
    mutationFn: (body: AddCurationRuleBody) => api.addCurationRule(body),
    onSuccess: invalidateDependents,
  });

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteCurationRule(id),
    onSuccess: () => {
      invalidateDependents();
      // Existing rules' affected-heartbeats previews may be stale.
      qc.invalidateQueries({ queryKey: qk.prefix.curationAffected });
    },
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
    onSuccess: () => {
      invalidateDependents();
      // The edited rule's affected-heartbeats preview may be stale.
      qc.invalidateQueries({ queryKey: qk.prefix.curationAffected });
    },
  });

  return { add, remove, edit };
}
