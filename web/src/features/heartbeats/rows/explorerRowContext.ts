import { createContext, useContext } from "react";
import type { GroupNode } from "@/features/heartbeats/explorerModel";
import type { SuppressInfo } from "@/features/heartbeats/useSuppression";

/**
 * Cross-cutting concerns shared by every explorer row kind (suppress/rename
 * actions + visible leaf columns), provided once by HeartbeatExplorerTable so
 * the per-kind row components only take the narrow props they actually render.
 */
export interface ExplorerRowContextValue {
  getSuppressInfo: (node: GroupNode) => SuppressInfo;
  toggleSuppress: (node: GroupNode, info: SuppressInfo) => void;
  suppressBusy: boolean;
  getRenamedTo: (node: GroupNode) => string | null;
  onRename: (node: GroupNode) => void;
  /** Ids of the currently visible leaf columns (drives colSpan + leaf cells). */
  visibleLeafColIds: string[];
}

export const ExplorerRowContext =
  createContext<ExplorerRowContextValue | null>(null);

export function useExplorerRowContext(): ExplorerRowContextValue {
  const ctx = useContext(ExplorerRowContext);
  if (!ctx) {
    throw new Error(
      "useExplorerRowContext must be used within HeartbeatExplorerTable",
    );
  }
  return ctx;
}

/** Indentation (px) per tree depth level, shared by all row kinds. */
export const INDENT = 18;
