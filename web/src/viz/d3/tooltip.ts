import * as d3 from "d3";
import { escapeHtml, tooltipHtml as tooltipHtmlSpec } from "./tooltipContent";
import type { TooltipSpec } from "./tooltipContent";

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
    // Slightly wider padding for multi-row layouts + a max-width so long names
    // wrap sanely rather than pushing the tooltip off the card.
    .style("padding", "8px 10px")
    .style("border-radius", "6px")
    .style("font-size", "12px")
    .style("line-height", "1.4")
    .style("max-width", "320px")
    .style("background", "var(--popover)")
    .style("color", "var(--popover-foreground)")
    .style("border", "1px solid var(--border)")
    .style("box-shadow", "0 4px 12px rgb(0 0 0 / 0.15)")
    .style("transition", "opacity 0.1s");
  return tip as unknown as TooltipSelection;
}

// Re-export for backwards compat with the many chart imports of
// `escapeHtml` from `@/viz/d3/tooltip`.
export { escapeHtml };

/**
 * Build tooltip markup. Two call shapes:
 *
 * 1. Structured: `tooltipHtml(spec: TooltipSpec)` — preferred going forward,
 *    escapes every field and produces the standard title/subtitle/rows/footer
 *    layout. Sibling bead gaka-7m4 consumes this signature via `spec.rows`.
 *
 * 2. Legacy positional: `tooltipHtml(title: string, ...rows)` where each row
 *    is either a plain string or `[label, value]`. Kept for zero-churn
 *    migration; every string is still escaped end-to-end.
 *
 * Chart data (names, branch names, file paths, languages…) derives from
 * heartbeats and is attacker-influenceable, so every call site must go
 * through this builder rather than hand-concatenating HTML.
 */
export function tooltipHtml(spec: TooltipSpec): string;
export function tooltipHtml(
  title: string,
  ...rows: Array<string | [label: string, value: string]>
): string;
export function tooltipHtml(
  first: TooltipSpec | string,
  ...rest: Array<string | [label: string, value: string]>
): string {
  if (typeof first === "object" && first !== null) {
    return tooltipHtmlSpec(first);
  }
  const title = first;
  const body = rest
    .map((r) =>
      typeof r === "string"
        ? escapeHtml(r)
        : `${escapeHtml(r[0])}: ${escapeHtml(r[1])}`,
    )
    .join("<br/>");
  return `<div style="font-weight:600">${escapeHtml(title)}</div>${body}`;
}

/**
 * Position the tooltip near the cursor while clamping it inside the container
 * so it never overflows the right or bottom edge. When the natural position
 * would spill off the right, we flip left of the cursor; likewise up when it
 * would spill off the bottom. Measurement happens after `html()` so the
 * computed bounding box reflects the rendered content.
 */
export function showTooltip(
  tip: TooltipSelection,
  container: HTMLElement,
  event: { clientX: number; clientY: number },
  html: string,
) {
  const [px, py] = d3.pointer(event, container);
  const node = tip
    .html(html)
    .style("opacity", "1")
    // Move offscreen while we measure to avoid a one-frame flicker at (0,0).
    .style("left", "-9999px")
    .style("top", "-9999px")
    .node() as HTMLDivElement | null;

  if (!node) return;

  const GAP = 12;
  const cw = container.clientWidth;
  const ch = container.clientHeight;
  const tw = node.offsetWidth;
  const th = node.offsetHeight;

  // Default: below-right of the cursor. Flip when we'd overflow.
  let x = px + GAP;
  if (x + tw > cw) x = px - GAP - tw; // flip left of cursor
  if (x < 0) x = Math.max(0, cw - tw); // last-ditch clamp inside container

  let y = py + GAP;
  if (y + th > ch) y = py - GAP - th; // flip above cursor
  if (y < 0) y = Math.max(0, ch - th);

  tip.style("left", `${x}px`).style("top", `${y}px`);
}

export function hideTooltip(tip: TooltipSelection) {
  tip.style("opacity", "0");
}
