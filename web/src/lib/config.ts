// Categorical chart palette: 14 maximally-distinct, dark-mode-legible hues,
// ordered so CONSECUTIVE indices are clearly different (no two blues/greens
// adjacent) — important for stacked bands (streamgraph), adjacent heatmap rows,
// and pie slices. Every color reads clearly on the near-black dark card while
// staying cohesive. This palette feeds every chart.
export const CHART_COLORS = [
  "#38bdf8", // sky blue
  "#f472b6", // pink
  "#34d399", // emerald
  "#fbbf24", // amber
  "#a78bfa", // violet
  "#fb7185", // rose
  "#22d3ee", // cyan
  "#a3e635", // lime
  "#fb923c", // orange
  "#818cf8", // indigo
  "#f0abfc", // fuchsia
  "#2dd4bf", // teal
  "#facc15", // yellow
  "#e879f9", // magenta
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
