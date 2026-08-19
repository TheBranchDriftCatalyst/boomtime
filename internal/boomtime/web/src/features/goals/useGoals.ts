// useGoals — React Query hooks for the goals feature (gaka-wpb).
//
// Mirrors the shape of useCuration:
//
//   useGoalsQuery()        → the list, keyed on qk.goals()
//   useGoalMutations()     → { create, update, remove, toggle }
//                             mutations, each invalidating the right
//                             surface after success.
//   useGoalProgress(id)    → single-goal progress with the 60s
//                             stale-while-revalidate policy mirrored
//                             on the client (staleTime = 60_000).
//   useAllGoalProgress()   → the batched dashboard map with the same
//                             staleTime.
//
// Invalidation policy:
//   - Any WRITE against a goal (create / update / delete / toggle)
//     invalidates qk.goals AND every goal-progress key (both the
//     per-id family and the batched form) — a rename or a
//     description edit doesn't affect progress arithmetic, but the
//     list surface DOES need to refetch to reflect the new row
//     shape, and per-goal tiles read from qk.goalProgress. Broad
//     invalidation is safe here (goals are user-scoped, low
//     cardinality, cheap).
//   - Heartbeat ingest invalidation is BACKEND-SIDE (the SaveHeartbeats
//     hook nulls last_evaluated_at); the FE doesn't need to
//     explicitly bust the goal-progress cache when it POSTs a
//     heartbeat because staleTime kicks in on the next refetch. If
//     that becomes a UX gap we can add a targeted invalidateQueries
//     inside the heartbeat mutation hook — deliberately kept out of
//     scope here.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import type { CreateGoalBody, UpdateGoalBody } from "@shared/types/api";
import { GoalCacheTTLMs } from "@boomtime/features/goals/constants";

export function useGoalsQuery() {
  return useQuery({
    queryKey: qk.goals(),
    queryFn: () => api.getGoals(),
    // The list surface is chatty (Settings landing page); short
    // staleTime keeps it fresh without hammering the endpoint.
    staleTime: 15_000,
  });
}

export function useGoalProgress(id: string, enabled = true) {
  return useQuery({
    queryKey: qk.goalProgress(id),
    queryFn: () => api.getGoalProgress(id),
    // Match the server-side cache TTL — dashboard reads within the
    // window are served from RQ's cache without a network round trip.
    // A goal spec edit invalidates the query below, so a fresh spec
    // is always reflected immediately.
    staleTime: GoalCacheTTLMs,
    enabled: !!id && enabled,
  });
}

export function useAllGoalProgress() {
  return useQuery({
    queryKey: qk.goalsProgress(),
    queryFn: () => api.getAllGoalProgress(),
    staleTime: GoalCacheTTLMs,
  });
}

export function useGoalMutations() {
  const qc = useQueryClient();

  // Broad invalidation: list + every per-goal progress + batched
  // progress. Goals are per-user, low cardinality; the cost is
  // minimal and the alternative (surgical per-id invalidation) risks
  // missing a tile that reads a stale batched entry.
  function invalidateDependents() {
    qc.invalidateQueries({ queryKey: qk.goals() });
    qc.invalidateQueries({ queryKey: qk.prefix.goalProgress });
    qc.invalidateQueries({ queryKey: qk.prefix.goalsProgress });
  }

  const create = useMutation({
    mutationFn: (body: CreateGoalBody) => api.createGoal(body),
    onSuccess: invalidateDependents,
  });

  const update = useMutation({
    mutationFn: (vars: { id: string; body: UpdateGoalBody }) =>
      api.updateGoal(vars.id, vars.body),
    onSuccess: invalidateDependents,
  });

  const remove = useMutation({
    mutationFn: (id: string) => api.deleteGoal(id),
    onSuccess: invalidateDependents,
  });

  const toggle = useMutation({
    mutationFn: (vars: { id: string; enabled?: boolean }) =>
      api.toggleGoal(vars.id, vars.enabled),
    onSuccess: invalidateDependents,
  });

  return { create, update, remove, toggle };
}
