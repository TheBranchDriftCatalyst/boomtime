import type { DrillPath, GroupStats } from "@/features/explorer/types";

// A single node in the unified explorer table. Group nodes come from the
// source's grouped fetch (one per axis level); a TERMINAL group (deepest axis)
// owns its paginated leaf rows directly. The synthetic leaf-group node now
// backs only the flat (zero-axis) "Table" view. Leaf nodes are the raw domain
// rows. All live in the same TanStack Table tree so a single table renders the
// whole drill-down (server-driven, lazy subRows).
export type ExplorerNode<Row = unknown> =
  | GroupNode
  | LeafGroupNode
  | LeafRowNode<Row>;

export interface GroupNode {
  kind: "group";
  id: string; // stable across renders (drill path + axis + value)
  axis: string;
  value: string | null; // raw group value (null => "(none)")
  // Per-group aggregates; always has "count", plus any configured rollups.
  stats: GroupStats;
  depth: number;
  // The drill path identifying this node (ancestors + this step). Applied to
  // children. Null groups add no step (ambiguous vs the "no filter" default).
  path: DrillPath;
  // The axis to group children by, or undefined if the next level is leaves.
  nextAxis?: string;
  // Can this node be drilled? Null non-leaf groups can't (ambiguous filter).
  drillable: boolean;
  // Loaded child nodes (undefined => not yet loaded).
  subRows?: ExplorerNode[];
}

// A synthetic node owning the paginated leaf rows for the flat (zero-axis)
// "Table" view. Drilled leaves attach their rows to the terminal group itself.
export interface LeafGroupNode {
  kind: "leafGroup";
  id: string;
  path: DrillPath;
  depth: number;
  subRows?: LeafRowNode[];
}

export interface LeafRowNode<Row = unknown> {
  kind: "leafRow";
  id: string;
  depth: number;
  row: Row;
}

export const NULL_TOKEN = "__null__";

export function groupNodeId(
  path: DrillPath,
  axis: string,
  value: string | null,
): string {
  const prefix = path.map((s) => `${s.dim}=${s.value}`).join("&");
  return `g:${prefix}|${axis}=${value ?? NULL_TOKEN}`;
}
