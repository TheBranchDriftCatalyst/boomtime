import { CHART_COLORS } from "@/lib/config";

/** The positional palette lookup every chart uses: CHART_COLORS[i % len]. */
export function colorAt(i: number): string {
  return CHART_COLORS[i % CHART_COLORS.length];
}

/**
 * Empty-cell floor for heatmap-style charts: a subtle tone clearly above
 * --card so the grid is visible but 0-value cells read as "empty". A fixed
 * rgb (not the oklch theme tokens, which d3.interpolateRgb can't parse) per
 * theme, so it can anchor color ramps.
 */
export function emptyFloor(): string {
  const dark = document.documentElement.classList.contains("dark");
  return dark ? "#232a36" : "#eceef2";
}

/**
 * PieChart's hide-tiny-slices threshold. Exported so callers replaying the
 * pie's palette (the Projects stacked columns) filter with the SAME cutoff
 * and can't drift from the pie's slice colors.
 */
export const MIN_SLICE_SECONDS = 60;

/**
 * The shared filter+order+palette contract: drop items below `minSeconds`,
 * keep the given order, and assign colors positionally via `colorAt`. Both a
 * chart and any call site that mirrors its coloring (e.g. PieChart and the
 * Projects stacked columns) must derive their name→color map from this
 * function so the two can never desync.
 */
export function paletteByName<T extends { name: string; totalSeconds: number }>(
  items: readonly T[],
  opts: { minSeconds?: number } = {},
): Map<string, string> {
  const { minSeconds = 0 } = opts;
  const palette = new Map<string, string>();
  items
    .filter((it) => it.totalSeconds >= minSeconds)
    .forEach((it, i) => palette.set(it.name, colorAt(i)));
  return palette;
}

/**
 * The shared category ordering contract (streamgraph, Overview stacked
 * columns): real categories by total desc, then the aggregated "Other (…)"
 * bucket(s) last, zero-total entries dropped. Feed the result to
 * `paletteByName` so order and color stay coupled.
 */
export function orderCategories<T extends { name: string; totalSeconds: number }>(
  items: readonly T[],
): T[] {
  const isOther = (r: T) => r.name.startsWith("Other (");
  return [
    ...items
      .filter((c) => !isOther(c))
      .sort((a, b) => b.totalSeconds - a.totalSeconds),
    ...items.filter(isOther),
  ].filter((c) => c.totalSeconds > 0);
}
