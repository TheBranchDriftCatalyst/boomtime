import type React from "react";

// The tight, domain-agnostic interface for the groupable explorer (boom-02sh).
// A domain supplies a DomainConfig<Row>; <GroupableExplorer config={cfg}/> is
// the only public surface. Nothing here knows about heartbeats, books, or any
// specific backend — the domain's TreeSource + columns + rollups drive it all.

// A groupable dimension (the axes offered in the "Group by" bar).
export interface Axis {
  id: string;
  label: string;
  // Section header the axis is clustered under in the picker (optional).
  section?: string;
}

// One leaf-row column. `get` is the sort/accessor; `render` (falling back to
// String(get)) draws the cell. `cellClassName`/`cellTitle` decorate the <td>.
export interface Column<Row> {
  id: string;
  header: string;
  get?: (r: Row) => unknown;
  defaultVisible?: boolean;
  render?: (r: Row) => React.ReactNode;
  cellClassName?: string;
  cellTitle?: (r: Row) => string | undefined;
}

// A finite per-domain rollup shown inline on each group row (alongside count).
export interface Rollup {
  id: string;
  label: string;
  format?: (n: number) => string;
}

// One step of a drill-down path: a concrete (dimension = value) filter.
export interface DrillStep {
  dim: string;
  value: string;
}
export type DrillPath = DrillStep[];

// Per-group aggregates. "count" is always present; each configured Rollup id
// may add another measure.
export type GroupStats = Record<string, number> & { count: number };

// A single group bucket returned by the source (value === null => "(none)").
export interface GroupResult {
  value: string | null;
  stats: GroupStats;
}

export interface GroupPage {
  groups: GroupResult[];
  truncated: boolean;
}

// A page of leaf rows under a fully-drilled path.
export interface LeafResult<Row> {
  rows: Row[];
  total: number;
  page: number;
  limit: number;
}

// The server-driven data source. The tree orchestrates nesting FE-side by
// issuing one grouped fetch per level with the accumulated drill path.
export interface TreeSource<Row> {
  fetchGroup(path: DrillPath, axis: string, rollups: string[]): Promise<GroupPage>;
  fetchLeaf(path: DrillPath, page: number, pageSize: number): Promise<LeafResult<Row>>;
}

// Decoration a domain injects onto a group row without the generic row knowing
// about it: inline badges (inside the label), trailing actions (right side),
// and a dimmed flag (e.g. rows a domain wants visually de-emphasized).
export interface GroupDecoration {
  dimmed?: boolean;
  badges?: React.ReactNode;
  actions?: React.ReactNode;
}

// Node-scoped group decorator. Imported by name as `GroupAction` in the plan.
export type GroupAction = (
  node: import("./explorerModel").GroupNode,
  path: DrillPath,
) => GroupDecoration;

// Trailing actions for a single leaf row.
export type RowAction<Row> = (row: Row) => React.ReactNode;

export interface ExplorerLabels {
  // Leaf-group row label ("Heartbeats", "Books", …).
  leafGroup: string;
  // First (tree) column header. Defaults to "Group / entity".
  treeHeader?: string;
  // Shown when there are zero group axes. When present the explorer renders
  // this hint instead of the flat leaf-rows view (heartbeats requires an axis;
  // domains that omit it get the flat "Table" view for free).
  addAxisHint?: React.ReactNode;
  // Copy for the root group-load failure.
  loadError?: React.ReactNode;
  // Full empty-state element (no groups in range).
  empty?: React.ReactNode;
}

export interface DomainConfig<Row> {
  axes: Axis[];
  defaultGroupBy: string[];
  columns: Column<Row>[];
  rollups: Rollup[];
  source: TreeSource<Row>;
  rowKey: (r: Row) => string;
  leafPageSize: number;
  labels: ExplorerLabels;
  // JSON leaf mode (a Table/JSON toggle). `renderJson` draws the payload; when
  // absent the explorer falls back to a plain <pre>.
  supportsJsonMode?: boolean;
  renderJson?: (value: unknown) => React.ReactNode;
  // Trailing per-leaf-row actions.
  rowActions?: RowAction<Row>;
  // Optional: clicking a leaf row calls this (e.g. open a detail panel). Undefined
  // → rows are not clickable (default). The JSON toggle + rowActions stop
  // propagation so they don't also trigger a select.
  onRowSelect?: (row: Row) => void;
  // A hook (called once inside the table body) yielding the per-group-node
  // decorator. It's a hook so the domain can pull in its own data via React
  // hooks (its own data sources + dialog state). A pluggable layer (e.g.
  // curation) supplies this; the generic explorer knows nothing of its content.
  useGroupDecorator?: () => GroupAction;
}
