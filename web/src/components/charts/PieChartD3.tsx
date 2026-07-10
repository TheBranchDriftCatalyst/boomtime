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
import type { PieChartProps } from "@/components/charts/types";
import type { ResourceStats } from "@/types/api";

/** D3 1:1 port of the breakdown pie chart (projects / languages). */
export function PieChartD3({ items, height = 320 }: PieChartProps) {
  const { ref, frame } = useChartFrame(height);
  const svgRef = useRef<SVGSVGElement | null>(null);

  const filtered = items.filter((v) => v.totalSeconds >= 60);

  useEffect(() => {
    const svg = d3.select(svgRef.current);
    svg.selectAll("*").remove();
    const container = ref.current;
    if (!container || frame.width === 0 || filtered.length === 0) return;

    const width = frame.width;
    const card = cssVar("--card");
    const radius = Math.min(width, height) / 2 - 6;

    const g = svg
      .attr("width", width)
      .attr("height", height)
      .append("g")
      .attr("transform", `translate(${width / 2},${height / 2})`);

    const pie = d3
      .pie<ResourceStats>()
      .sort(null)
      .value((d) => d.totalSeconds);
    const arcs = pie(filtered);
    const total = d3.sum(filtered, (d) => d.totalSeconds) || 1;

    const arc = d3.arc<d3.PieArcDatum<ResourceStats>>().innerRadius(0).outerRadius(radius);
    const labelArc = d3
      .arc<d3.PieArcDatum<ResourceStats>>()
      .innerRadius(radius * 0.6)
      .outerRadius(radius * 0.6);

    const tip: TooltipSelection = createTooltip(container);

    g.selectAll("path")
      .data(arcs)
      .join("path")
      .attr("d", arc)
      .attr("fill", (_d, i) => CHART_COLORS[i % CHART_COLORS.length])
      .attr("stroke", card)
      .attr("stroke-width", 1)
      .on("mousemove", (event, d) => {
        showTooltip(
          tip,
          container,
          event,
          `<div style="font-weight:600">${d.data.name}</div>${secondsToHms(
            d.data.totalSeconds,
          )}`,
        );
      })
      .on("mouseleave", () => hideTooltip(tip));

    // Percentage labels for slices with enough room (matches Apex dataLabels).
    g.selectAll("text.slice")
      .data(arcs)
      .join("text")
      .attr("class", "slice")
      .attr("transform", (d) => `translate(${labelArc.centroid(d)})`)
      .attr("text-anchor", "middle")
      .attr("dominant-baseline", "central")
      .attr("fill", "#fff")
      .style("font-size", "11px")
      .style("pointer-events", "none")
      .text((d) => {
        const pct = (d.data.totalSeconds / total) * 100;
        return pct >= 5 ? `${pct.toFixed(1)}%` : "";
      });

    return () => {
      tip.remove();
    };
  }, [ref, filtered, height, frame.width, frame.themeKey]);

  if (filtered.length === 0) return <EmptyChart height={height} />;

  return (
    <div ref={ref} style={{ position: "relative", width: "100%", height }}>
      <svg ref={svgRef} />
    </div>
  );
}
