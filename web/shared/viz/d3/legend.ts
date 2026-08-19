import * as d3 from "d3";

export interface LegendItem {
  label: string;
  color: string;
}

export interface RenderLegendOptions {
  /** Left edge of the legend group (usually the plot's left margin). */
  x: number;
  /** Top offset of the legend group (default 0). */
  y?: number;
  /** Label color (resolved --muted-foreground). */
  fg: string;
  /** Available width — items past this are collapsed into "+N more". */
  maxWidth: number;
  /** Horizontal padding added after each measured label (default 28). */
  gap?: number;
}

/**
 * Measured-offset swatch legend across the top of a chart (10×10 rounded
 * swatch + label per item, advancing by the label's measured width).
 *
 * Overflow fix (small intentional visual improvement): items that would run
 * past `maxWidth` — previously silently clipped off the right edge of the
 * svg — are dropped and replaced with a "+N more" marker.
 */
export function renderLegend(
  svg: d3.Selection<SVGSVGElement, unknown, null, undefined>,
  items: LegendItem[],
  opts: RenderLegendOptions,
): void {
  const { x, y = 0, fg, maxWidth, gap = 28 } = opts;
  const legend = svg.append("g").attr("transform", `translate(${x},${y})`);
  let offset = 0;
  for (let i = 0; i < items.length; i++) {
    const item = legend
      .append("g")
      .attr("transform", `translate(${offset},0)`);
    item
      .append("rect")
      .attr("width", 10)
      .attr("height", 10)
      .attr("rx", 2)
      .attr("y", 3)
      .attr("fill", items[i].color);
    const label = item
      .append("text")
      .attr("x", 14)
      .attr("y", 12)
      .attr("fill", fg)
      .style("font-size", "11px")
      .text(items[i].label);
    const textW = label.node()?.getComputedTextLength() ?? 40;
    // Always keep the first item; collapse the rest once they'd overflow.
    if (i > 0 && offset + 14 + textW > maxWidth) {
      item.remove();
      legend
        .append("text")
        .attr("x", offset)
        .attr("y", 12)
        .attr("fill", fg)
        .style("font-size", "11px")
        .text(`+${items.length - i} more`);
      return;
    }
    offset += textW + gap;
  }
}
