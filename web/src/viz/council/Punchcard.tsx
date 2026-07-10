import { useEffect, useRef } from "react";
import * as d3 from "d3";
import { CHART_COLORS } from "@/lib/config";
import { secondsToHms } from "@/lib/utils";
import { cssVar, useChartFrame } from "@/viz/d3/useChartFrame";
import {
  createTooltip,
  hideTooltip,
  showTooltip,
  type TooltipSelection,
} from "@/viz/d3/tooltip";
import { EmptyChart } from "@/viz/d3/EmptyChart";
import type { PunchcardPayload } from "@/types/api";

interface PunchcardProps {
  data: PunchcardPayload | undefined;
  height?: number;
}

const DOW = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

/**
 * Classic 7x24 punchcard: rows = day of week (Sun..Sat), cols = hour (0..23),
 * bubble radius ∝ seconds. Times are UTC (backend aggregates in UTC); a small
 * note communicates that. Dark-mode native; responsive.
 */
export function Punchcard({ data, height = 260 }: PunchcardProps) {
  const { ref, frame } = useChartFrame(height);
  const svgRef = useRef<SVGSVGElement | null>(null);

  useEffect(() => {
    const svg = d3.select(svgRef.current);
    svg.selectAll("*").remove();
    const container = ref.current;
    if (!container || frame.width === 0 || !data || data.cells.length === 0)
      return;

    const fg = cssVar("--muted-foreground");
    const width = frame.width;
    const margin = { top: 8, right: 12, bottom: 22, left: 34 };
    const innerW = width - margin.left - margin.right;
    const innerH = height - margin.top - margin.bottom;

    const g = svg
      .attr("width", width)
      .attr("height", height)
      .append("g")
      .attr("transform", `translate(${margin.left},${margin.top})`);

    const x = d3.scaleBand<number>().domain(d3.range(24)).range([0, innerW]).padding(0.1);
    const y = d3.scaleBand<number>().domain(d3.range(7)).range([0, innerH]).padding(0.1);

    const maxSeconds = data.maxSeconds || d3.max(data.cells, (c) => c.seconds) || 1;
    const rMax = Math.min(x.bandwidth(), y.bandwidth()) / 2 - 1;
    const r = d3.scaleSqrt().domain([0, maxSeconds]).range([0, rMax]);

    // Hour axis (every 3h).
    g.append("g")
      .attr("transform", `translate(0,${innerH})`)
      .call(d3.axisBottom(x).tickValues(d3.range(0, 24, 3)).tickFormat((d) => String(d)))
      .call((sel) => sel.select(".domain").remove())
      .selectAll("text")
      .attr("fill", fg)
      .style("font-size", "10px");

    // Day-of-week axis.
    g.append("g")
      .call(d3.axisLeft(y).tickFormat((d) => DOW[Number(d)]))
      .call((sel) => sel.select(".domain").remove())
      .selectAll("text")
      .attr("fill", fg)
      .style("font-size", "10px");

    const tip: TooltipSelection = createTooltip(container);

    g.selectAll("circle.punch")
      .data(data.cells.filter((c) => c.seconds > 0))
      .join("circle")
      .attr("class", "punch")
      .attr("cx", (c) => (x(c.hour) ?? 0) + x.bandwidth() / 2)
      .attr("cy", (c) => (y(c.dow) ?? 0) + y.bandwidth() / 2)
      .attr("r", (c) => Math.max(1.5, r(c.seconds)))
      .attr("fill", CHART_COLORS[0])
      .attr("fill-opacity", 0.85)
      .on("mousemove", (event, c) => {
        showTooltip(
          tip,
          container,
          event,
          `<div style="font-weight:600">${DOW[c.dow]} ${String(c.hour).padStart(2, "0")}:00 UTC</div>` +
            secondsToHms(c.seconds),
        );
      })
      .on("mouseleave", () => hideTooltip(tip));

    return () => {
      tip.remove();
    };
  }, [data, height, frame.width, frame.themeKey, ref]);

  if (!data || data.cells.length === 0) return <EmptyChart height={height} />;

  return (
    <div ref={ref} style={{ position: "relative", width: "100%", height }}>
      <svg ref={svgRef} />
      <p className="mt-1 text-xs text-muted-foreground">Times shown in UTC.</p>
    </div>
  );
}
