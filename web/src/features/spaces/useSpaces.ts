import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import type { AddSpaceRuleBody } from "@/types/api";

export function useSpaces() {
  return useQuery({
    queryKey: qk.spaces(),
    queryFn: () => api.getSpaces(),
  });
}

export function useSpace(id: number | string | null | undefined) {
  return useQuery({
    queryKey: qk.space(id),
    enabled: id != null,
    queryFn: () => api.getSpace(id as number | string),
  });
}

export function useSpaceMutations() {
  const qc = useQueryClient();

  // Invalidate the space list, one space's detail, and every Space-scoped
  // dashboard (?space=…) so open scoped views refetch after any rule change.
  function invalidateForSpace(id?: number | string) {
    qc.invalidateQueries({ queryKey: qk.spaces() });
    if (id != null) {
      qc.invalidateQueries({ queryKey: qk.space(id) });
    }
    for (const key of qk.dashboardDependents) {
      qc.invalidateQueries({ queryKey: key });
    }
  }

  const create = useMutation({
    mutationFn: (name: string) => api.createSpace(name),
    onSuccess: (space) => invalidateForSpace(space.id),
  });

  const rename = useMutation({
    mutationFn: (vars: {
      id: number | string;
      name?: string;
      position?: number;
    }) => api.renameSpace(vars.id, { name: vars.name, position: vars.position }),
    onSuccess: (_data, vars) => invalidateForSpace(vars.id),
  });

  const remove = useMutation({
    mutationFn: (id: number | string) => api.deleteSpace(id),
    onSuccess: (_data, id) => invalidateForSpace(id),
  });

  const addRule = useMutation({
    mutationFn: (vars: { id: number | string; body: AddSpaceRuleBody }) =>
      api.addSpaceRule(vars.id, vars.body),
    onSuccess: (_data, vars) => invalidateForSpace(vars.id),
  });

  const deleteRule = useMutation({
    mutationFn: (vars: { id: number | string; rid: number | string }) =>
      api.deleteSpaceRule(vars.id, vars.rid),
    onSuccess: (_data, vars) => invalidateForSpace(vars.id),
  });

  return { create, rename, remove, addRule, deleteRule };
}
