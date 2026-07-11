import * as d3 from "d3";

// A single shared tooltip element, styled with theme tokens.
export type TooltipSelection = d3.Selection<
  HTMLDivElement,
  unknown,
  null,
  undefined
>;

export function createTooltip(container: HTMLElement): TooltipSelection {
  const tip = d3
    .select(container)
    .append("div")
    .attr("class", "gk-d3-tooltip")
    .style("position", "absolute")
    .style("pointer-events", "none")
    .style("opacity", "0")
    .style("z-index", "50")
    .style("padding", "6px 10px")
    .style("border-radius", "6px")
    .style("font-size", "12px")
    .style("line-height", "1.4")
    .style("white-space", "nowrap")
    .style("background", "var(--popover)")
    .style("color", "var(--popover-foreground)")
    .style("border", "1px solid var(--border)")
    .style("box-shadow", "0 4px 12px rgb(0 0 0 / 0.15)")
    .style("transition", "opacity 0.1s");
  return tip as unknown as TooltipSelection;
}

/** Escape a string for safe interpolation into tooltip HTML. */
export function escapeHtml(s: string): string {
  return s
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

/**
 * Build the standard tooltip markup — a bold title line followed by plain
 * rows — escaping ALL interpolated text. Rows are either a plain string or a
 * `[label, value]` pair rendered as `label: value`. Chart data (names, branch
 * names, file paths, languages…) derives from heartbeats and is
 * attacker-influenceable, so every call site must go through this builder
 * (or escapeHtml) rather than hand-concatenating HTML.
 */
export function tooltipHtml(
  title: string,
  ...rows: Array<string | [label: string, value: string]>
): string {
  const body = rows
    .map((r) =>
      typeof r === "string"
        ? escapeHtml(r)
        : `${escapeHtml(r[0])}: ${escapeHtml(r[1])}`,
    )
    .join("<br/>");
  return `<div style="font-weight:600">${escapeHtml(title)}</div>${body}`;
}

export function showTooltip(
  tip: TooltipSelection,
  container: HTMLElement,
  event: { clientX: number; clientY: number },
  html: string,
) {
  const [x, y] = d3.pointer(event, container);
  tip
    .html(html)
    .style("opacity", "1")
    .style("left", `${x + 12}px`)
    .style("top", `${y + 12}px`);
}

export function hideTooltip(tip: TooltipSelection) {
  tip.style("opacity", "0");
}
