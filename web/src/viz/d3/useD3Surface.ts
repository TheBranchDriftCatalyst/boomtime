import { useEffect, useRef } from "react";
import * as d3 from "d3";
import { useChartFrame, type ChartFrame } from "./useChartFrame";
import {
  createTooltip,
  hideTooltip,
  showTooltip,
  type TooltipSelection,
} from "./tooltip";

export interface Margin {
  top: number;
  right: number;
  bottom: number;
  left: number;
}

/** Everything a draw callback needs, pre-ritualized. */
export interface D3SurfaceCtx {
  /** The svg root (height attr set; width attr set too unless `sizeToFrame: false`). */
  svg: d3.Selection<SVGSVGElement, unknown, null, undefined>;
  /** Margin-translated plot group (at 0,0 when no margin was given). */
  g: d3.Selection<SVGGElement, unknown, null, undefined>;
  /** Measured container width. */
  width: number;
  height: number;
  /** width/height minus margins (equal to width/height when no margin). */
  innerW: number;
  innerH: number;
  container: HTMLDivElement;
  /** Show the chart tooltip (lazily created on first call) at the pointer. */
  showTip: (event: { clientX: number; clientY: number }, html: string) => void;
  hideTip: () => void;
}

export interface D3Surface {
  ref: React.RefObject<HTMLDivElement | null>;
  svgRef: React.RefObject<SVGSVGElement | null>;
  frame: ChartFrame;
  height: number;
}

export interface D3SurfaceOptions {
  height: number;
  /**
   * Plot margins. Must be render-stable VALUES (a fresh literal per render is
   * fine — it is not a dependency); the draw does not re-run on margin change.
   */
  margin?: Margin;
  /**
   * Default true: the svg width tracks the measured frame width and the draw
   * re-runs on width changes. Set false for content-sized charts
   * (ContributionCalendar) that own the svg width themselves and must draw
   * even before the first width measurement.
   */
  sizeToFrame?: boolean;
}

/**
 * The shared D3 chart ritual: measure via useChartFrame, clear + size the
 * svg, append a margin-translated plot group, manage the tooltip lifecycle
 * (lazy create, remove on cleanup), and re-run `draw` when the chart's data
 * deps, height, frame width (unless `sizeToFrame: false`), or theme change —
 * the exact redraw semantics every chart previously hand-rolled.
 *
 * Pass ONLY data dependencies in `deps`; height/width/theme are appended
 * here. Render the returned surface with `<ChartSurface surface={…} />`.
 */
export function useD3Surface(
  options: D3SurfaceOptions,
  draw: (ctx: D3SurfaceCtx) => void,
  deps: React.DependencyList,
): D3Surface {
  const { height, margin, sizeToFrame = true } = options;
  const { ref, frame } = useChartFrame(height);
  const svgRef = useRef<SVGSVGElement | null>(null);

  // Read the latest draw/margin from refs so they aren't (unstatable)
  // dependencies — semantics match the old inline effects, where the closure
  // was recreated per render but only deps triggered a redraw.
  const drawRef = useRef(draw);
  drawRef.current = draw;
  const marginRef = useRef(margin);
  marginRef.current = margin;

  useEffect(() => {
    const node = svgRef.current;
    d3.select(node).selectAll("*").remove();
    const container = ref.current;
    if (!node || !container) return;
    if (sizeToFrame && frame.width === 0) return;

    const svg = d3.select(node);
    const width = frame.width;
    const m = marginRef.current;
    svg.attr("height", height);
    if (sizeToFrame) svg.attr("width", width);
    const g = svg
      .append("g")
      .attr("transform", `translate(${m?.left ?? 0},${m?.top ?? 0})`);

    let tip: TooltipSelection | null = null;
    const showTip = (
      event: { clientX: number; clientY: number },
      html: string,
    ) => {
      tip ??= createTooltip(container);
      showTooltip(tip, container, event, html);
    };
    const hideTip = () => {
      if (tip) hideTooltip(tip);
    };

    drawRef.current({
      svg,
      g,
      width,
      height,
      innerW: width - (m ? m.left + m.right : 0),
      innerH: height - (m ? m.top + m.bottom : 0),
      container,
      showTip,
      hideTip,
    });

    return () => {
      tip?.remove();
    };
    // Chart data deps are spread in; size/theme deps appended (frame.width
    // only when the svg tracks the frame). `ref`/`svgRef` are stable.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...deps, height, sizeToFrame, sizeToFrame ? frame.width : 0, frame.themeKey]);

  return { ref, svgRef, frame, height };
}
