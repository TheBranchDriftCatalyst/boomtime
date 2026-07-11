import { createContext, useContext } from "react";
import type { GroupNode } from "@/features/heartbeats/explorerModel";
import type { SuppressInfo } from "@/features/heartbeats/useSuppression";
import type {
  SpaceMembership,
  SpaceOption,
} from "@/features/heartbeats/useSpaceMembership";

/**
 * Cross-cutting concerns shared by every explorer row kind (suppress/rename/
 * add-to-Space actions + visible leaf columns), provided once by
 * HeartbeatExplorerTable so the per-kind row components only take the narrow
 * props they actually render.
 */
export interface ExplorerRowContextValue {
  getSuppressInfo: (node: GroupNode) => SuppressInfo;
  toggleSuppress: (node: GroupNode, info: SuppressInfo) => void;
  suppressBusy: boolean;
  getRenamedTo: (node: GroupNode) => string | null;
  onRename: (node: GroupNode) => void;
  // Space membership: which Spaces this value already belongs to (exact rules),
  // the Spaces it can be added to, and the add action.
  getSpacesFor: (node: GroupNode) => SpaceMembership[];
  canAddToSpace: (node: GroupNode) => boolean;
  spaceOptions: SpaceOption[];
  addToSpace: (node: GroupNode, spaceId: number, spaceName: string) => void;
  spaceBusy: boolean;
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
