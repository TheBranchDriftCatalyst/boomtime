import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import type { AddSpaceRuleBody } from "@/types/api";

// Dashboard query keys whose results are scoped by a Space (?space=…). Any
// Space/rule change invalidates these so open scoped views refetch. Mirrors the
// curation DEPENDENT_KEYS.
const DEPENDENT_KEYS = [
  ["stats"],
  ["project-stats"],
  ["projects"],
  ["leaderboards"],
  ["timeline"],
  ["punchcard"],
  ["sessions"],
  ["momentum"],
  ["cross-project-files"],
];

export function useSpaces() {
  return useQuery({
    queryKey: ["spaces"],
    queryFn: () => api.getSpaces(),
  });
}

export function useSpace(id: number | string | null | undefined) {
  return useQuery({
    queryKey: ["space", id != null ? String(id) : null],
    enabled: id != null,
    queryFn: () => api.getSpace(id as number | string),
  });
}

export function useSpaceMutations() {
  const qc = useQueryClient();

  // Invalidate the space list, one space's detail, and every scoped dashboard.
  function invalidateForSpace(id?: number | string) {
    qc.invalidateQueries({ queryKey: ["spaces"] });
    if (id != null) {
      qc.invalidateQueries({ queryKey: ["space", String(id)] });
    }
    for (const key of DEPENDENT_KEYS) {
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
