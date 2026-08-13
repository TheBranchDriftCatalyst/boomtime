// booksExplore.ts — model + formatting for the Books "Explore" mode
// (heartbeats-explorer-style group-by-dimension over reading data).
//
// Explore drives ONE call to the cross-domain query DSL (runQuery) per
// dimension×measure pick and renders the returned groups as a ranked bar table.
// This module holds only the pure pieces (dimensions, measures, formatters) so
// the component + its test stay thin and the whitelist below can be asserted.
//
// The dimension whitelist MIRRORS the backend registry (internal/query/
// domains.go): the `books` measure groups by source/status/series/author/genre;
// `runtime` (sum of runtime_min) groups by source/status/series/genre — NOT
// author. Asking the backend to group `runtime` by `author` is a 400, so the UI
// must never offer it. Keep these two lists in lockstep with that registry.
import {
  BookOpen,
  Layers,
  Library,
  Tags,
  User,
  type LucideIcon,
} from "lucide-react";
import type { QuerySpec } from "@/lib/queryApi";

export type ReadingDim = "source" | "status" | "series" | "author" | "genre";
export type BooksMeasure = "books" | "runtime";

export interface DimMeta {
  dim: ReadingDim;
  label: string;
  icon: LucideIcon;
}

// Ordered as they appear in the chip bar.
export const READING_DIMS: DimMeta[] = [
  { dim: "source", label: "Source", icon: Layers },
  { dim: "status", label: "Status", icon: BookOpen },
  { dim: "series", label: "Series", icon: Library },
  { dim: "author", label: "Author", icon: User },
  { dim: "genre", label: "Genre", icon: Tags },
];

export interface MeasureMeta {
  measure: BooksMeasure;
  label: string;
  /** Dimensions this measure can group by (backend registry whitelist). */
  dims: ReadingDim[];
}

export const MEASURES: MeasureMeta[] = [
  {
    measure: "books",
    label: "Books",
    dims: ["source", "status", "series", "author", "genre"],
  },
  {
    measure: "runtime",
    label: "Runtime",
    // No "author" — runtime lives on reading_items but the registry omits it.
    dims: ["source", "status", "series", "genre"],
  },
];

/** How many named groups to keep before rolling the rest into "Other". */
export const TOP_N = 12;

/** The sentinel key the backend uses for the bucket roll-up row (always last). */
export const OTHER_KEY = "Other";

export function measureMeta(measure: BooksMeasure): MeasureMeta {
  return MEASURES.find((m) => m.measure === measure) ?? MEASURES[0];
}

/** True when `dim` is a legal group-by for `measure` (mirrors the backend). */
export function dimAllowed(dim: ReadingDim, measure: BooksMeasure): boolean {
  return measureMeta(measure).dims.includes(dim);
}

/**
 * Build the QuerySpec for a single group-by pick. Bucketing forces the backend
 * to return value-desc with a single trailing "Other" row; `limit` = TOP_N + 1
 * keeps every named group plus that roll-up (a smaller limit would clip Other).
 */
export function buildBooksGroupSpec(
  dim: ReadingDim,
  measure: BooksMeasure,
): QuerySpec {
  return {
    domain: "reading",
    measure,
    group: dim,
    bucket: { topN: TOP_N, other: true },
    sort: { field: "value", desc: true },
    limit: TOP_N + 1,
  };
}

/** Format a group's value for the given measure (count vs. minutes → h/m). */
export function formatMeasureValue(value: number, measure: BooksMeasure): string {
  if (measure === "runtime") return formatMinutes(value);
  return Math.round(value).toLocaleString();
}

/** Compact "runtime minutes" → "1h 20m" / "45m" / "3h". */
export function formatMinutes(min: number): string {
  const total = Math.max(0, Math.round(min));
  if (total < 60) return `${total}m`;
  const h = Math.floor(total / 60);
  const m = total % 60;
  return m ? `${h}h ${m}m` : `${h}h`;
}

/** Display label for a group key — empty (null dimension) reads as "(none)". */
export function groupKeyLabel(key: string): string {
  return key.trim() === "" ? "(none)" : key;
}

export function isOtherRow(key: string): boolean {
  return key === OTHER_KEY;
}
