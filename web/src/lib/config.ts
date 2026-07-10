// Chart color palette carried over verbatim from hakatime's config.js so charts
// keep the same look, applied within the modernized Tailwind design.
export const CHART_COLORS = [
  "#03a9f4",
  "#B0DAF1",
  "#84B082",
  "#775DD0",
  "#FF9800",
  "#A5978B",
  "#FD6A6A",
  "#69D2E7",
  "#C5D86D",
  "#3E1929",
  "#60E1E0",
  "#F7C1BB",
  "#E2C044",
  "#C4BBAF",
];

export const NO_DATA = {
  text: "No data available",
  style: { fontSize: "16px" },
};

// Date-range presets (in days) shown in the toolbar.
export const DATE_RANGE_PRESETS = [7, 15, 30, 60];

// Time-limit (heartbeat gap coalescing) options in minutes.
export const TIME_LIMIT_OPTIONS = [5, 10, 15, 20, 30];

// Selectable windows for the recent timeline chart (in hours).
export const TIMELINE_HOUR_OPTIONS = [6, 12, 24, 48];

export const DEFAULT_NUM_DAYS = 15;
export const DEFAULT_TIME_LIMIT = 15;
