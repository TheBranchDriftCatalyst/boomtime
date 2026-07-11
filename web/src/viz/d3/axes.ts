import * as d3 from "d3";

// The axis <g> selections our charts produce (g.append("g").call(axis…)).
export type AxisSelection = d3.Selection<SVGGElement, unknown, null, undefined>;

export interface AxisTokens {
  /** Tick-label color (usually the resolved --muted-foreground). */
  fg: string;
  /** Domain-line color (usually the resolved --border); only read for `domain: "line"`. */
  border?: string;
}

export interface StyleAxisOptions {
  /**
   * What to do with the axis domain path: "remove" (default) drops it,
   * "line" strokes it with `tokens.border`.
   */
  domain?: "remove" | "line";
  /** Tick-label font size (default "11px"). */
  fontSize?: string;
}

/**
 * The shared post-render axis styling ritual: strip or recolor the domain
 * path, then color/size the tick labels. Returns the selection so charts can
 * chain extra per-axis tweaks (title elements, rotated labels, …).
 */
export function styleAxis(
  sel: AxisSelection,
  tokens: AxisTokens,
  opts: StyleAxisOptions = {},
): AxisSelection {
  const { domain = "remove", fontSize = "11px" } = opts;
  if (domain === "remove") sel.select(".domain").remove();
  else sel.select(".domain").attr("stroke", tokens.border ?? tokens.fg);
  sel.selectAll("text").attr("fill", tokens.fg).style("font-size", fontSize);
  return sel;
}

export interface GridlinesOptions {
  /**
   * Which side the generating axis hangs off: "left" (default) draws
   * horizontal lines across the plot, "bottom" draws vertical ones.
   */
  orient?: "left" | "bottom";
  /** Inner dimension the lines span (innerW for "left", innerH for "bottom"). */
  span: number;
  /** Line color (usually the resolved --border). */
  stroke: string;
  /** Tick count (default 5). */
  ticks?: number;
  /** Vertical offset of the group — bottom axes sit at innerH. */
  offsetY?: number;
  /**
   * Optional visible tick labels. Default is a pure gridline group with no
   * text (the chart draws its labeled axis separately).
   */
  tickFormat?: (v: d3.NumberValue) => string;
  /** Label color, required when `tickFormat` is given. */
  fg?: string;
  /** Label font size (default "11px"). */
  fontSize?: string;
}

/**
 * Dashed gridlines built from a full-bleed axis (tickSize = -span), domain
 * removed. With `tickFormat`/`fg` it doubles as a combined gridline+label
 * axis (the horizontal-bar charts' bottom axis).
 */
export function gridlines(
  g: AxisSelection,
  scale: d3.AxisScale<d3.NumberValue>,
  opts: GridlinesOptions,
): AxisSelection {
  const {
    orient = "left",
    span,
    stroke,
    ticks = 5,
    offsetY,
    tickFormat,
    fg,
    fontSize = "11px",
  } = opts;
  const axis = (orient === "left" ? d3.axisLeft(scale) : d3.axisBottom(scale))
    .ticks(ticks)
    .tickSize(-span)
    .tickFormat(tickFormat ?? (() => ""));
  const sel = g.append("g");
  if (offsetY !== undefined) sel.attr("transform", `translate(0,${offsetY})`);
  sel
    .call(axis)
    .call((s) => s.select(".domain").remove())
    .call((s) =>
      s.selectAll("line").attr("stroke", stroke).attr("stroke-dasharray", "4"),
    );
  if (tickFormat && fg) {
    sel.selectAll("text").attr("fill", fg).style("font-size", fontSize);
  }
  return sel;
}

/**
 * Thin a tick-value list down to at most ~`max` entries by keeping every
 * n-th value (the shared `Math.ceil(n / 8)` date-axis pattern).
 */
export function thinnedDateTicks<T>(values: readonly T[], max = 8): T[] {
  const every = Math.ceil(values.length / max) || 1;
  return values.filter((_, i) => i % every === 0);
}

/** Shared day-axis label formatter — "07 Mar". */
export const formatDay = d3.timeFormat("%d %b");

/**
 * Seconds → hours tick formatter. Label text is parameterized because charts
 * intentionally differ ("3.5" vs "3.5h" vs "4h") — pass the chart's current
 * `decimals`/`suffix` to keep its visible output byte-identical.
 */
export function hoursTickFormat(
  opts: { decimals?: number; suffix?: string } = {},
): (v: d3.NumberValue) => string {
  const { decimals = 1, suffix = "" } = opts;
  return (v) => `${(Number(v) / 3600).toFixed(decimals)}${suffix}`;
}
