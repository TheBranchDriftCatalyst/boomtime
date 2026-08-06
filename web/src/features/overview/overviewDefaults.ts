// overviewDefaults (gaka-7uc, Phase 3) — the default widget layout for the
// composable Overview dashboard grid. Reproduces OverviewDashboard.tsx's
// CURRENT vertical arrangement + relative sizes on a 12-col grid so that when
// Phase 4 swaps the static ChartCard stack for a draggable widget grid, a user
// with no saved layout sees the same order and proportions they see today.
//
// `i` is the widget kind id (matches web/src/features/widgets/catalog.ts).
// `view` pins the chart-toggle view where the kind has one. Heights are in the
// grid's row-height units; widths are 12-col fractions.
import type { GridLayoutItem } from "@/lib/grid";

export const OVERVIEW_DEFAULT_LAYOUT: GridLayoutItem[] = [
  // Stat strip — 4 KPI tiles across the top.
  { i: "overview-stats", x: 0, y: 0, w: 12, h: 2 },

  // Ambient overlays (self-hide when the range has no data).
  { i: "ai-assistance", x: 0, y: 2, w: 12, h: 3 },
  { i: "wellness", x: 0, y: 5, w: 12, h: 3 },

  // Category breakdown + streak banner.
  { i: "category-breakdown", x: 0, y: 8, w: 12, h: 3 },
  { i: "streak-banner", x: 0, y: 11, w: 12, h: 2 },

  // Flagship contribution calendar (full width).
  { i: "activity-heatmap", x: 0, y: 13, w: 12, h: 3 },

  // Total activity (8) | Project breakdown pie (4).
  { i: "overview-total-activity", x: 0, y: 16, w: 8, h: 4 },
  { i: "top-projects", x: 8, y: 16, w: 4, h: 4, view: "pie" },

  // Cumulative area (6) | Category streamgraph (6).
  { i: "cumulative-area", x: 0, y: 20, w: 6, h: 4 },
  { i: "category-streamgraph", x: 6, y: 20, w: 6, h: 4 },

  // Activity-per-project (6) | Activity-per-language (6) heatmaps.
  { i: "heatmap-projects", x: 0, y: 24, w: 6, h: 3 },
  { i: "heatmap-languages", x: 6, y: 24, w: 6, h: 3 },

  // Patterns: punchcard (6) | momentum (6).
  { i: "punchcard", x: 0, y: 27, w: 6, h: 4, view: "heatmap" },
  { i: "momentum", x: 6, y: 27, w: 6, h: 4 },

  // Deep-work sessions (full width).
  { i: "deep-work", x: 0, y: 31, w: 12, h: 3 },

  // Recent timeline (full width; carries its own Last-N-hours control).
  { i: "overview-timeline", x: 0, y: 34, w: 12, h: 4 },
];
