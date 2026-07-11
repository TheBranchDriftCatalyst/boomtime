import { useCallback, useEffect, useMemo, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import type {
  HeartbeatAxis,
  HeartbeatFilters,
  HeartbeatGroupPayload,
  HeartbeatListPayload,
} from "@/types/api";
import { LEAF_PAGE_SIZE } from "@/features/heartbeats/axes";
import {
  groupNodeId,
  type ExplorerNode,
  type GroupNode,
  type LeafGroupNode,
  type LeafRowNode,
} from "@/features/heartbeats/explorerModel";

interface Params {
  axes: HeartbeatAxis[];
  start: string;
  end: string;
  timeLimit: number;
  entity: string;
}

export interface ChildState {
  loading: boolean;
  error: boolean;
  children?: ExplorerNode[];
  truncated?: boolean;
}

// Leaf pagination is tracked per leaf-group id.
export interface LeafPageState {
  page: number; // 1-based (matches backend)
  total: number;
  limit: number;
}

/**
 * Server-driven lazy tree for the heartbeats explorer. Owns:
 *  - the root group query (first axis),
 *  - a per-node children cache (fetched on first expand),
 *  - leaf pagination per fully-drilled path.
 *
 * It assembles a plain ExplorerNode[] tree (with populated `subRows` only for
 * expanded+loaded nodes) that feeds one TanStack Table via getSubRows +
 * getExpandedRowModel. React Query provides caching/dedup keyed by filter path.
 */
export function useExplorerTree({ axes, start, end, timeLimit, entity }: Params) {
  const qc = useQueryClient();
  const rootAxis = axes[0];

  const [rootState, setRootState] = useState<ChildState>({
    loading: false,
    error: false,
  });
  // node id -> loaded children state.
  const [childCache, setChildCache] = useState<Record<string, ChildState>>({});
  // leaf-group id -> pagination.
  const [leafPages, setLeafPages] = useState<Record<string, LeafPageState>>({});

  // Reset caches whenever the query inputs change (axes/range/entity/timeLimit).
  const inputKey = `${axes.join(">")}|${start}|${end}|${timeLimit}|${entity}`;
  useEffect(() => {
    setChildCache({});
    setLeafPages({});
    setRootState({ loading: false, error: false });
  }, [inputKey]);

  const fetchGroup = useCallback(
    (axis: HeartbeatAxis, filters: HeartbeatFilters) =>
      qc.fetchQuery({
        queryKey: qk.hbExploreGroup(axis, filters, start, end, timeLimit),
        queryFn: () =>
          api.groupHeartbeats({ groupBy: axis, start, end, timeLimit, filters }),
        staleTime: 30_000,
      }),
    [qc, start, end, timeLimit],
  );

  const fetchLeaf = useCallback(
    (filters: HeartbeatFilters, page: number) =>
      qc.fetchQuery({
        queryKey: qk.hbExploreList(filters, entity, start, end, page),
        queryFn: () =>
          api.listHeartbeats({
            start,
            end,
            filters,
            entity,
            page,
            limit: LEAF_PAGE_SIZE,
          }),
        staleTime: 30_000,
      }),
    [qc, start, end, entity],
  );

  // Build group child nodes from a payload for a given depth/axis path.
  const buildGroupChildren = useCallback(
    (
      payload: HeartbeatGroupPayload,
      depth: number,
      axisIndex: number,
      parentFilters: HeartbeatFilters,
    ): ExplorerNode[] => {
      const axis = axes[axisIndex];
      const nextAxis = axes[axisIndex + 1];
      const isLastAxis = axisIndex === axes.length - 1;
      return payload.groups.map((g): GroupNode => {
        const isNull = g.value == null;
        // Skip adding a null filter (ambiguous vs the backend's absent = no
        // filter convention). Null non-leaf groups can't be drilled.
        const childFilters: HeartbeatFilters = isNull
          ? parentFilters
          : { ...parentFilters, [axis]: g.value as string };
        const drillable = isLastAxis || !isNull;
        return {
          kind: "group",
          id: groupNodeId(parentFilters, axis, g.value),
          axis,
          value: g.value,
          count: g.count,
          seconds: g.seconds,
          firstSeen: g.firstSeen,
          lastSeen: g.lastSeen,
          depth,
          childFilters,
          nextAxis: isLastAxis ? undefined : nextAxis,
          drillable,
        };
      });
    },
    [axes],
  );

  // Load root groups.
  const loadRoot = useCallback(async () => {
    if (!rootAxis) return;
    setRootState((s) => ({ ...s, loading: true, error: false }));
    try {
      const payload = await fetchGroup(rootAxis, {});
      const children = buildGroupChildren(payload, 0, 0, {});
      setRootState({
        loading: false,
        error: false,
        children,
        truncated: payload.truncated,
      });
    } catch {
      setRootState({ loading: false, error: true });
    }
  }, [rootAxis, fetchGroup, buildGroupChildren]);

  useEffect(() => {
    void loadRoot();
    // loadRoot depends on inputKey-derived callbacks.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [inputKey]);

  // Expand handler: lazily load a node's children on first expand.
  const ensureLoaded = useCallback(
    async (node: ExplorerNode) => {
      if (node.kind === "leafRow") return;

      if (node.kind === "group") {
        if (childCache[node.id]?.children || childCache[node.id]?.loading)
          return;
        // Last axis -> its child is a single leaf-group node.
        if (!node.nextAxis) {
          const leafId = `leaf:${node.id}`;
          setChildCache((c) => ({ ...c, [node.id]: { loading: true, error: false } }));
          const leafGroup: LeafGroupNode = {
            kind: "leafGroup",
            id: leafId,
            filters: node.childFilters,
            depth: node.depth + 1,
          };
          setChildCache((c) => ({
            ...c,
            [node.id]: { loading: false, error: false, children: [leafGroup] },
          }));
          return;
        }
        // Next axis group level.
        setChildCache((c) => ({ ...c, [node.id]: { loading: true, error: false } }));
        try {
          const axisIndex = axes.indexOf(node.nextAxis);
          const payload = await fetchGroup(node.nextAxis, node.childFilters);
          const children = buildGroupChildren(
            payload,
            node.depth + 1,
            axisIndex,
            node.childFilters,
          );
          setChildCache((c) => ({
            ...c,
            [node.id]: {
              loading: false,
              error: false,
              children,
              truncated: payload.truncated,
            },
          }));
        } catch {
          setChildCache((c) => ({ ...c, [node.id]: { loading: false, error: true } }));
        }
        return;
      }

      // leafGroup: load the current page of heartbeat rows.
      if (node.kind === "leafGroup") {
        if (childCache[node.id]?.loading) return;
        const page = leafPages[node.id]?.page ?? 1;
        setChildCache((c) => ({ ...c, [node.id]: { loading: true, error: false } }));
        try {
          const payload: HeartbeatListPayload = await fetchLeaf(node.filters, page);
          const rows: LeafRowNode[] = payload.items.map((r) => ({
            kind: "leafRow",
            id: `row:${node.id}:${r.id}`,
            depth: node.depth + 1,
            row: r,
          }));
          setLeafPages((p) => ({
            ...p,
            [node.id]: { page, total: payload.total, limit: payload.limit },
          }));
          setChildCache((c) => ({
            ...c,
            [node.id]: { loading: false, error: false, children: rows },
          }));
        } catch {
          setChildCache((c) => ({ ...c, [node.id]: { loading: false, error: true } }));
        }
      }
    },
    [axes, childCache, leafPages, fetchGroup, fetchLeaf, buildGroupChildren],
  );

  // Change the page for a leaf-group and refetch.
  const setLeafPage = useCallback(
    async (leafGroup: LeafGroupNode, page: number) => {
      setChildCache((c) => ({ ...c, [leafGroup.id]: { loading: true, error: false } }));
      try {
        const payload = await fetchLeaf(leafGroup.filters, page);
        const rows: LeafRowNode[] = payload.items.map((r) => ({
          kind: "leafRow",
          id: `row:${leafGroup.id}:${r.id}`,
          depth: leafGroup.depth + 1,
          row: r,
        }));
        setLeafPages((p) => ({
          ...p,
          [leafGroup.id]: { page, total: payload.total, limit: payload.limit },
        }));
        setChildCache((c) => ({
          ...c,
          [leafGroup.id]: { loading: false, error: false, children: rows },
        }));
      } catch {
        setChildCache((c) => ({ ...c, [leafGroup.id]: { loading: false, error: true } }));
      }
    },
    [fetchLeaf],
  );

  // Recursively attach loaded children to build the tree TanStack consumes.
  const attach = useCallback(
    (nodes: ExplorerNode[]): ExplorerNode[] =>
      nodes.map((n) => {
        if (n.kind === "leafRow") return n;
        const state = childCache[n.id];
        if (!state?.children) return n;
        return { ...n, subRows: attach(state.children) } as ExplorerNode;
      }),
    [childCache],
  );

  const tree = useMemo<ExplorerNode[]>(
    () => (rootState.children ? attach(rootState.children) : []),
    [rootState.children, attach],
  );

  return {
    tree,
    rootLoading: rootState.loading,
    rootError: rootState.error,
    rootTruncated: rootState.truncated,
    childCache,
    leafPages,
    ensureLoaded,
    setLeafPage,
    reloadRoot: loadRoot,
  };
}
