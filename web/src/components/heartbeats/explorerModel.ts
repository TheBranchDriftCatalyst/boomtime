import type { HeartbeatAxis, HeartbeatFilters, HeartbeatRow } from "@/types/api";

// A single node in the unified explorer table. Group nodes come from the group
// endpoint (one per axis level); leaf nodes are raw heartbeat rows fetched once
// the deepest axis is expanded. Both live in the same TanStack Table tree so a
// single table renders the whole drill-down (server-driven, lazy subRows).
export type ExplorerNode = GroupNode | LeafGroupNode | LeafRowNode;

export interface GroupNode {
  kind: "group";
  id: string; // stable across renders (filter path + axis + value)
  axis: HeartbeatAxis;
  value: string | null; // raw group value (null => "(none)")
  count: number;
  seconds: number;
  firstSeen: string;
  lastSeen: string;
  depth: number;
  // Filters accumulated from ancestors + this node, applied to children.
  childFilters: HeartbeatFilters;
  // The axis to group children by, or undefined if the next level is leaves.
  nextAxis?: HeartbeatAxis;
  // Can this node be drilled? Null non-leaf groups can't (ambiguous filter).
  drillable: boolean;
  // Loaded child nodes (undefined => not yet loaded).
  subRows?: ExplorerNode[];
}

// A synthetic node under the last group level that owns the paginated leaf
// heartbeat rows for the fully-drilled filter path.
export interface LeafGroupNode {
  kind: "leafGroup";
  id: string;
  filters: HeartbeatFilters;
  depth: number;
  subRows?: LeafRowNode[];
}

export interface LeafRowNode {
  kind: "leafRow";
  id: string;
  depth: number;
  row: HeartbeatRow;
}

export const NULL_TOKEN = "__null__";

export function groupNodeId(
  filters: HeartbeatFilters,
  axis: HeartbeatAxis,
  value: string | null,
): string {
  const path = Object.entries(filters)
    .map(([k, v]) => `${k}=${v}`)
    .join("&");
  return `g:${path}|${axis}=${value ?? NULL_TOKEN}`;
}
