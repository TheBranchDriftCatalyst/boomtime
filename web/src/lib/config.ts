// SYNTHWAVE / OUTRUN categorical chart palette: 14 vivid neon hues tuned to
// pop on the near-black violet dark cards while staying cohesive. Ordered so
// CONSECUTIVE indices are clearly different (no two similar neons adjacent) —
// important for stacked bands (streamgraph), adjacent heatmap rows, and pie
// slices. This palette feeds every chart.
export const CHART_COLORS = [
  "#05d9e8", // electric cyan
  "#ff2d95", // neon magenta
  "#a3ff3c", // neon lime
  "#b967ff", // electric purple
  "#ffb13d", // amber
  "#3b7bff", // neon blue
  "#ff5e7e", // hot pink
  "#2dffb3", // spring aqua
  "#ff8f1f", // neon orange
  "#e94bff", // fuchsia
  "#00f0ff", // cyan glow
  "#ffe94d", // acid yellow
  "#7a5cff", // indigo violet
  "#ff3caa", // magenta rose
];

// Date-range presets (in days) shown in the toolbar.
export const DATE_RANGE_PRESETS = [7, 15, 30, 60];

// Time-limit (heartbeat gap coalescing) options in minutes.
export const TIME_LIMIT_OPTIONS = [5, 10, 15, 20, 30];

// Selectable windows for the recent timeline chart (in hours).
export const TIMELINE_HOUR_OPTIONS = [6, 12, 24, 48];

export const DEFAULT_NUM_DAYS = 15;
export const DEFAULT_TIME_LIMIT = 15;
