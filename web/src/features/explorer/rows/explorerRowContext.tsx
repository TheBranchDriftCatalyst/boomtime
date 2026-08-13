import { createContext, useContext } from "react";
import type { Column, Rollup, RowAction } from "@/features/explorer/types";

/**
 * Cross-cutting data shared by every explorer row kind (visible columns,
 * rollups, leaf-group label, JSON mode + renderer, per-row actions, axis
 * labels), provided once by ExplorerTable so the per-kind row components only
 * take the narrow props they actually render. Domain decorations are injected
 * separately as a per-node GroupDecoration, keeping these rows domain-agnostic.
 */
export interface ExplorerRowContextValue {
  // Currently visible leaf columns, in display order.
  columns: Column<unknown>[];
  rollups: Rollup[];
  leafGroupLabel: string;
  // Whether the domain supports JSON at all (drives the per-row raw drawer).
  supportsJson: boolean;
  // Whether the table-level Table/JSON toggle is currently on JSON.
  jsonMode: boolean;
  renderJson: (value: unknown) => React.ReactNode;
  rowActions?: RowAction<unknown>;
  labelForAxis: (id: string) => string;
}

export const ExplorerRowContext =
  createContext<ExplorerRowContextValue | null>(null);

export function useExplorerRowContext(): ExplorerRowContextValue {
  const ctx = useContext(ExplorerRowContext);
  if (!ctx) {
    throw new Error("useExplorerRowContext must be used within ExplorerTable");
  }
  return ctx;
}

/** Indentation (px) per tree depth level, shared by all row kinds. */
export const INDENT = 18;

/** Default JSON renderer when a domain doesn't provide one. */
export function defaultRenderJson(value: unknown): React.ReactNode {
  return (
    <pre className="max-h-[28rem] overflow-auto rounded-md border bg-muted/40 p-3 font-mono text-xs leading-relaxed">
      {JSON.stringify(value, null, 2)}
    </pre>
  );
}
