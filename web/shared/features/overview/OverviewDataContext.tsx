// OverviewDataContext (boom-38v) — the shared inputs every Overview widget
// needs: the stats window (tr), the "Recent timeline" hours, and the optional
// Space scope. Provided once by OverviewDashboard; consumed by the per-widget
// self-fetch hooks in overviewWidgets.ts.
//
// The point of this seam: the Phase-3 self-fetching widget renderer runs the
// SAME queries the legacy inline OverviewDashboard runs (identical qk.* keys →
// react-query dedupes), so a dashboard rendered as draggable widgets costs no
// extra network and shares the same cache as the current static layout.
import { createContext, useContext } from "react";
import type { TimeRangeControls } from "@shared/hooks/useTimeRange";

export interface OverviewDataContextValue {
  /** The stats-window controls (start/end/timeLimit + setters). */
  tr: TimeRangeControls;
  /** "Recent timeline" window, in hours. */
  timelineHours: number;
  setTimelineHours: (n: number) => void;
  /** When set, every query is scoped to this Space. Omitted → global Overview. */
  space?: string;
}

const OverviewDataContext = createContext<OverviewDataContextValue | null>(null);

export const OverviewDataProvider = OverviewDataContext.Provider;

// This file exports a provider + a hook together (same pattern as useAuth.tsx);
// the mixed export only affects Fast-Refresh granularity in dev.
// eslint-disable-next-line react-refresh/only-export-components
export function useOverviewData(): OverviewDataContextValue {
  const ctx = useContext(OverviewDataContext);
  if (!ctx) {
    throw new Error("useOverviewData must be used within an OverviewDataProvider");
  }
  return ctx;
}
