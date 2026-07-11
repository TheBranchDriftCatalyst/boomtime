import { useCallback, useMemo, useState } from "react";
import { LEAF_COLUMNS } from "@/features/heartbeats/leafColumns";
import type {
  ExplorerNode,
  GroupNode,
  LeafGroupNode,
} from "@/features/heartbeats/explorerModel";

export interface LeafSort {
  id: string;
  desc: boolean;
}

/**
 * Client-side sorting of loaded leaf pages (the server has no sort param).
 * Returns the sorted tree plus a toggleSort(columnId) handler that cycles
 * asc → desc per column.
 */
export function useLeafSort(tree: ExplorerNode[]) {
  const [sorting, setSorting] = useState<LeafSort | null>(null);

  // Sort each loaded leaf page client-side (server has no sort param).
  const sortedTree = useMemo(() => {
    if (!sorting) return tree;
    const col = LEAF_COLUMNS.find((c) => c.id === sorting.id);
    if (!col) return tree;
    const cmp = (a: ExplorerNode, b: ExplorerNode) => {
      if (a.kind !== "leafRow" || b.kind !== "leafRow") return 0;
      const va = col.get(a.row);
      const vb = col.get(b.row);
      const sa = va == null ? "" : String(va);
      const sb = vb == null ? "" : String(vb);
      const r = sa.localeCompare(sb, undefined, { numeric: true });
      return sorting.desc ? -r : r;
    };
    const walk = (nodes: ExplorerNode[]): ExplorerNode[] =>
      nodes.map((n) => {
        if (n.kind === "leafRow") return n;
        const sub = (n as GroupNode | LeafGroupNode).subRows;
        if (!sub) return n;
        const nextSub =
          n.kind === "leafGroup" ? [...sub].sort(cmp) : walk(sub);
        return { ...n, subRows: nextSub } as ExplorerNode;
      });
    return walk(tree);
  }, [tree, sorting]);

  const toggleSort = useCallback(
    (id: string) =>
      setSorting((s) =>
        s?.id === id ? { id, desc: !s.desc } : { id, desc: false },
      ),
    [],
  );

  return { sorting, toggleSort, sortedTree };
}
