import { useCallback, useEffect, useMemo, useState } from "react";
import type {
  DomainConfig,
  DrillPath,
  GroupPage,
  LeafResult,
} from "@/features/explorer/types";
import {
  groupNodeId,
  type ExplorerNode,
  type GroupNode,
  type LeafGroupNode,
  type LeafRowNode,
} from "@/features/explorer/explorerModel";

interface Params<Row> {
  config: DomainConfig<Row>;
  // Ordered group-by axis ids. Empty => flat leaf rows (when flatWhenEmpty).
  axes: string[];
  // Opaque token; when it (or `axes`) changes every cache is dropped and the
  // root reloads. The domain folds its query inputs (range/entity/…) into it.
  resetKey: string;
  // With zero axes: eager-load leaf rows directly (the flat "Table" view) when
  // true; do nothing (the consumer shows an "add an axis" hint) when false.
  flatWhenEmpty: boolean;
}

export interface ChildState {
  loading: boolean;
  error: boolean;
  children?: ExplorerNode[];
  truncated?: boolean;
}

// Stable id of the synthetic leaf-group that owns the flat (zero-axis) root
// table. Sharing a real LeafGroupNode id routes the flat root through the exact
// same leaf pagination path as a fully-drilled leaf (setLeafPage/leafPages/
// LeafGroupRow) instead of hard-capping the flat table at one leafPageSize page.
export const ROOT_LEAF_ID = "leaf:__root__";

// Leaf pagination is tracked per leaf-group id.
export interface LeafPageState {
  page: number; // 1-based (matches backend)
  total: number;
  limit: number;
}

/**
 * Server-driven lazy tree for the groupable explorer. Owns:
 *  - the root group query (first axis),
 *  - a per-node children cache (fetched on first expand),
 *  - leaf pagination per fully-drilled path.
 *
 * It assembles a plain ExplorerNode[] tree (with populated `subRows` only for
 * expanded+loaded nodes) that feeds one TanStack Table via getSubRows +
 * getExpandedRowModel. The DomainConfig's TreeSource provides caching/dedup.
 * With zero axes the root is a single flat leaf-group (the flat "Table" view).
 */
export function useExplorerTree<Row>({
  config,
  axes,
  resetKey,
  flatWhenEmpty,
}: Params<Row>) {
  const { source, rowKey, leafPageSize } = config;
  const rootAxis = axes[0];
  const rollupIds = useMemo(
    () => config.rollups.map((r) => r.id),
    [config.rollups],
  );

  const [rootState, setRootState] = useState<ChildState>({
    loading: false,
    error: false,
  });
  // node id -> loaded children state.
  const [childCache, setChildCache] = useState<Record<string, ChildState>>({});
  // leaf-group id -> pagination.
  const [leafPages, setLeafPages] = useState<Record<string, LeafPageState>>({});

  // Reset caches whenever the query inputs change (axes/resetKey).
  const inputKey = `${axes.join(">")}|${resetKey}`;
  useEffect(() => {
    setChildCache({});
    setLeafPages({});
    setRootState({ loading: false, error: false });
  }, [inputKey]);

  const fetchGroup = useCallback(
    (axis: string, path: DrillPath): Promise<GroupPage> =>
      source.fetchGroup(path, axis, rollupIds),
    [source, rollupIds],
  );

  const fetchLeaf = useCallback(
    (path: DrillPath, page: number): Promise<LeafResult<Row>> =>
      source.fetchLeaf(path, page, leafPageSize),
    [source, leafPageSize],
  );

  // Build group child nodes from a page for a given depth/axis level.
  const buildGroupChildren = useCallback(
    (
      page: GroupPage,
      depth: number,
      axisIndex: number,
      parentPath: DrillPath,
    ): ExplorerNode[] => {
      const axis = axes[axisIndex];
      const nextAxis = axes[axisIndex + 1];
      const isLastAxis = axisIndex === axes.length - 1;
      return page.groups.map((g): GroupNode => {
        const isNull = g.value == null;
        // Skip adding a null step (ambiguous vs the backend's absent = no
        // filter convention). Null non-leaf groups can't be drilled.
        const childPath: DrillPath = isNull
          ? parentPath
          : [...parentPath, { dim: axis, value: g.value as string }];
        const drillable = isLastAxis || !isNull;
        return {
          kind: "group",
          id: groupNodeId(parentPath, axis, g.value),
          axis,
          value: g.value,
          stats: g.stats,
          depth,
          path: childPath,
          nextAxis: isLastAxis ? undefined : nextAxis,
          drillable,
        };
      });
    },
    [axes],
  );

  // Load root groups. With zero axes there is no grouping: eagerly load the
  // first page of leaf rows as top-level nodes (the flat "Table" view — rows
  // render directly, no drill-down).
  const loadRoot = useCallback(async () => {
    if (axes.length === 0) {
      // No axes and the consumer shows an "add an axis" hint: fetch nothing.
      if (!flatWhenEmpty) {
        setRootState({ loading: false, error: false, children: [] });
        return;
      }
      setRootState((s) => ({ ...s, loading: true, error: false }));
      try {
        const payload = await fetchLeaf([], 1);
        // Empty range: no leaf group, so the consumer's empty state renders
        // instead of a stray "0 rows" header.
        if (payload.total === 0 && payload.rows.length === 0) {
          setLeafPages((p) => ({
            ...p,
            [ROOT_LEAF_ID]: { page: 1, total: 0, limit: payload.limit },
          }));
          setRootState({ loading: false, error: false, children: [] });
          return;
        }
        // A single synthetic root leaf-group owns the paginated flat table —
        // the same LeafGroupNode the drilled leaves use, so setLeafPage /
        // leafPages / LeafGroupRow drive its Prev/Next pager unchanged. Its
        // page-1 rows live in childCache under ROOT_LEAF_ID; `attach` hangs
        // them off the leaf group as subRows. ExplorerTable seeds this row's
        // expanded state so the flat table shows rows immediately.
        const rows: LeafRowNode<Row>[] = payload.rows.map((r) => ({
          kind: "leafRow",
          id: `row:${ROOT_LEAF_ID}:${rowKey(r)}`,
          depth: 1,
          row: r,
        }));
        const leafGroup: LeafGroupNode = {
          kind: "leafGroup",
          id: ROOT_LEAF_ID,
          path: [],
          depth: 0,
        };
        setLeafPages((p) => ({
          ...p,
          [ROOT_LEAF_ID]: { page: 1, total: payload.total, limit: payload.limit },
        }));
        setChildCache((c) => ({
          ...c,
          [ROOT_LEAF_ID]: { loading: false, error: false, children: rows },
        }));
        setRootState({ loading: false, error: false, children: [leafGroup] });
      } catch {
        setRootState({ loading: false, error: true });
      }
      return;
    }
    if (!rootAxis) return;
    setRootState((s) => ({ ...s, loading: true, error: false }));
    try {
      const page = await fetchGroup(rootAxis, []);
      const children = buildGroupChildren(page, 0, 0, []);
      setRootState({
        loading: false,
        error: false,
        children,
        truncated: page.truncated,
      });
    } catch {
      setRootState({ loading: false, error: true });
    }
  }, [
    axes.length,
    flatWhenEmpty,
    rootAxis,
    fetchGroup,
    fetchLeaf,
    buildGroupChildren,
    rowKey,
  ]);

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
            path: node.path,
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
          const page = await fetchGroup(node.nextAxis, node.path);
          const children = buildGroupChildren(
            page,
            node.depth + 1,
            axisIndex,
            node.path,
          );
          setChildCache((c) => ({
            ...c,
            [node.id]: {
              loading: false,
              error: false,
              children,
              truncated: page.truncated,
            },
          }));
        } catch {
          setChildCache((c) => ({ ...c, [node.id]: { loading: false, error: true } }));
        }
        return;
      }

      // leafGroup: load the current page of leaf rows.
      if (node.kind === "leafGroup") {
        if (childCache[node.id]?.loading) return;
        const page = leafPages[node.id]?.page ?? 1;
        setChildCache((c) => ({ ...c, [node.id]: { loading: true, error: false } }));
        try {
          const payload = await fetchLeaf(node.path, page);
          const rows: LeafRowNode<Row>[] = payload.rows.map((r) => ({
            kind: "leafRow",
            id: `row:${node.id}:${rowKey(r)}`,
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
    [axes, childCache, leafPages, fetchGroup, fetchLeaf, buildGroupChildren, rowKey],
  );

  // Change the page for a leaf-group and refetch.
  const setLeafPage = useCallback(
    async (leafGroup: LeafGroupNode, page: number) => {
      setChildCache((c) => ({ ...c, [leafGroup.id]: { loading: true, error: false } }));
      try {
        const payload = await fetchLeaf(leafGroup.path, page);
        const rows: LeafRowNode<Row>[] = payload.rows.map((r) => ({
          kind: "leafRow",
          id: `row:${leafGroup.id}:${rowKey(r)}`,
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
    [fetchLeaf, rowKey],
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
