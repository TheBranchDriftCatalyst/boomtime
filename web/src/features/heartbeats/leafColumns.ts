import type { HeartbeatRow } from "@/types/api";

export interface LeafColumn {
  id: string;
  header: string;
  // Value extractor for sorting/display.
  get: (r: HeartbeatRow) => string | number | boolean | null;
  // Whether shown by default in the column-visibility menu.
  defaultVisible: boolean;
  align?: "left" | "right";
  mono?: boolean;
}

// Leaf heartbeat columns (order = display order). Sortable + toggleable.
export const LEAF_COLUMNS: LeafColumn[] = [
  { id: "time", header: "Time", get: (r) => r.time, defaultVisible: true },
  { id: "entity", header: "Entity", get: (r) => r.entity, defaultVisible: true, mono: true },
  { id: "language", header: "Language", get: (r) => r.language, defaultVisible: true },
  { id: "project", header: "Project", get: (r) => r.project, defaultVisible: true },
  { id: "branch", header: "Branch", get: (r) => r.branch, defaultVisible: true },
  { id: "editor", header: "Editor", get: (r) => r.editor, defaultVisible: true },
  { id: "category", header: "Category", get: (r) => r.category, defaultVisible: false },
  { id: "isWrite", header: "Write", get: (r) => r.isWrite, defaultVisible: true },
];

export function leafCellText(colId: string, r: HeartbeatRow): string {
  const col = LEAF_COLUMNS.find((c) => c.id === colId);
  if (!col) return "";
  if (colId === "time") {
    const d = new Date(r.time);
    return Number.isNaN(d.getTime()) ? r.time : d.toLocaleString();
  }
  if (colId === "isWrite") {
    return r.isWrite == null ? "-" : r.isWrite ? "yes" : "no";
  }
  const v = col.get(r);
  return v == null || v === "" ? "(none)" : String(v);
}
