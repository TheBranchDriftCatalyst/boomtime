import { useCallback, useMemo, useState } from "react";
import type { Column } from "@/features/explorer/types";
import type {
  ExplorerNode,
  GroupNode,
  LeafGroupNode,
  LeafRowNode,
} from "@/features/explorer/explorerModel";

export interface LeafSort {
  id: string;
  desc: boolean;
}

/**
 * Client-side sorting of loaded leaf pages (the server has no sort param).
 * Returns the sorted tree plus a toggleSort(columnId) handler that cycles
 * asc → desc per column, comparing via the column's `get` accessor.
 */
export function useLeafSort<Row>(
  tree: ExplorerNode[],
  columns: Column<Row>[],
) {
  const [sorting, setSorting] = useState<LeafSort | null>(null);

  const sortedTree = useMemo(() => {
    if (!sorting) return tree;
    const col = columns.find((c) => c.id === sorting.id);
    if (!col?.get) return tree;
    const get = col.get;
    const cmp = (a: ExplorerNode, b: ExplorerNode) => {
      if (a.kind !== "leafRow" || b.kind !== "leafRow") return 0;
      const va = get((a as LeafRowNode<Row>).row);
      const vb = get((b as LeafRowNode<Row>).row);
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
        // A leaf owner's children are leaf rows: sort them here. That's either
        // the synthetic flat-root leaf-group or a terminal group (no nextAxis,
        // which now holds its rows directly). Non-terminal groups recurse.
        const ownsLeafRows =
          n.kind === "leafGroup" ||
          (n.kind === "group" && !(n as GroupNode).nextAxis);
        const nextSub = ownsLeafRows ? [...sub].sort(cmp) : walk(sub);
        return { ...n, subRows: nextSub } as ExplorerNode;
      });
    return walk(tree);
  }, [tree, sorting, columns]);

  const toggleSort = useCallback(
    (id: string) =>
      setSorting((s) =>
        s?.id === id ? { id, desc: !s.desc } : { id, desc: false },
      ),
    [],
  );

  return { sorting, toggleSort, sortedTree };
}
