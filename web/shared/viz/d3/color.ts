import { CHART_COLORS } from "@shared/lib/config";

/**
 * Number of `--chart-N` tokens each theme in catalyst-ui declares.
 * All 10 themes (arasaka, boomtime, catalyst, dracula, dungeon, gold,
 * laracon, nature, netflix, nord) ship `--chart-1..12` per the theme
 * registry contract. Charts wrap around this window on i >= 12.
 */
const THEME_CHART_TOKEN_COUNT = 12;

/**
 * Read the active theme's `--chart-N` token via `getComputedStyle` at
 * call time so theme swaps (which flip the html className) are picked up
 * on the NEXT chart render — no invalidation plumbing required.
 *
 * SSR-safe: returns `null` when `document` isn't defined so the caller
 * falls back to the pure hex constant.
 */
function readChartToken(index: number): string | null {
  if (typeof document === "undefined") return null;
  const tokenName = `--chart-${(index % THEME_CHART_TOKEN_COUNT) + 1}`;
  const raw = getComputedStyle(document.documentElement)
    .getPropertyValue(tokenName)
    .trim();
  return raw.length > 0 ? raw : null;
}

/**
 * Normalize any CSS color value to an rgb hex string d3 can interpolate.
 *
 * The gotcha: our theme tokens are `oklch(…)` (catalyst-ui's normalized
 * color space). d3.interpolateRgb goes through d3.color, which does NOT
 * understand oklch/lch/color() — feeding those in returns null and every
 * downstream cell renders as the same (failed) color. Symptom: heatmap
 * grids that are all-uniform-floor, streamgraphs that go monochrome.
 *
 * The fillStyle round-trip is NOT enough — modern Chromium keeps
 * `oklch(...)` as the canonical form when you set + read fillStyle. Real
 * conversion requires actually rasterizing: paint the color to a 1×1
 * canvas pixel, read the ImageData back as sRGB integers, format as
 * `#rrggbb`. Canvas rasterization always produces sRGB, so this converts
 * any browser-parseable color (oklch, lch, color(), lab, hsl, hex) to
 * something d3.interpolateRgb will happily consume.
 *
 * Memoized because it allocates a canvas + touches ImageData; the
 * palette has ~12 distinct inputs per render.
 *
 * SSR-safe: returns the input verbatim when `document` isn't defined so
 * caller falls through to the fallback hex constant.
 */
const rgbCache = new Map<string, string>();
function normalizeToRgb(css: string): string {
  if (typeof document === "undefined") return css;
  const cached = rgbCache.get(css);
  if (cached) return cached;
  const c = document.createElement("canvas");
  c.width = 1;
  c.height = 1;
  const ctx = c.getContext("2d");
  if (!ctx) return css;
  ctx.fillStyle = css;
  ctx.fillRect(0, 0, 1, 1);
  const [r, g, b] = ctx.getImageData(0, 0, 1, 1).data;
  const hex =
    "#" + [r, g, b].map((v) => v.toString(16).padStart(2, "0")).join("");
  rgbCache.set(css, hex);
  return hex;
}

/**
 * Positional palette lookup used by every chart. Reads the active theme's
 * `--chart-N` token; falls back to the legacy hardcoded SYNTHWAVE hex hue
 * when the token is unset (SSR, pre-mount, or a theme that somehow
 * shipped without chart tokens).
 *
 * Always returns an rgb-shape string (canvas-normalized) so downstream
 * d3.interpolateRgb / d3.color consumers work regardless of the theme's
 * source color space (oklch/hsl/hex all become rgb here). See boom-538
 * for the original token migration and the d3-oklch-parse regression
 * that followed.
 *
 * `getComputedStyle` is cheap (browser-cached until layout invalidates)
 * so per-call cost is negligible vs. a chart draw.
 */
export function colorAt(i: number): string {
  const raw = readChartToken(i) ?? CHART_COLORS[i % CHART_COLORS.length];
  return normalizeToRgb(raw);
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
