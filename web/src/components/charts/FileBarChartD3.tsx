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
import type { FileBarChartProps } from "@/components/charts/types";

// Shorten a file path to "<parent>/<file>" for the in-bar label.
function shortLabel(val: string): string {
  const parts = val.split("/").filter(Boolean);
  const filename = parts[parts.length - 1] ?? val;
  const parent = parts[parts.length - 2];
  return parent ? `${parent}/${filename}` : filename;
}

/** D3 1:1 port of the most-active-files horizontal bar chart (top 10). */
export function FileBarChartD3({ files, height = 380 }: FileBarChartProps) {
  const { ref, frame } = useChartFrame(height);
  const svgRef = useRef<SVGSVGElement | null>(null);

  const top = [...files]
    .filter((v) => v.totalSeconds / 3600 > 0 && !v.name.startsWith("Other ("))
    .sort((a, b) => b.totalSeconds - a.totalSeconds)
    .slice(0, 10);

  useEffect(() => {
    const svg = d3.select(svgRef.current);
    svg.selectAll("*").remove();
    const container = ref.current;
    if (!container || frame.width === 0 || top.length === 0) return;

    const fg = cssVar("--muted-foreground");
    const border = cssVar("--border");
    const width = frame.width;
    const margin = { top: 8, right: 16, bottom: 30, left: 8 };
    const innerW = width - margin.left - margin.right;
    const innerH = height - margin.top - margin.bottom;

    const g = svg
      .attr("width", width)
      .attr("height", height)
      .append("g")
      .attr("transform", `translate(${margin.left},${margin.top})`);

    const y = d3
      .scaleBand<number>()
      .domain(d3.range(top.length))
      .range([0, innerH])
      .padding(0.2);
    const xMax = d3.max(top, (d) => d.totalSeconds) ?? 0;
    const x = d3.scaleLinear().domain([0, xMax || 1]).nice().range([0, innerW]);

    // X gridlines + hours axis at the bottom.
    g.append("g")
      .attr("transform", `translate(0,${innerH})`)
      .call(
        d3
          .axisBottom(x)
          .ticks(5)
          .tickSize(-innerH)
          .tickFormat((v) => (Number(v) / 3600).toFixed(1)),
      )
      .call((sel) => sel.select(".domain").remove())
      .call((sel) =>
        sel
          .selectAll("line")
          .attr("stroke", border)
          .attr("stroke-dasharray", "4"),
      )
      .selectAll("text")
      .attr("fill", fg)
      .style("font-size", "11px");

    g.append("text")
      .attr("x", innerW)
      .attr("y", innerH + 26)
      .attr("text-anchor", "end")
      .attr("fill", fg)
      .style("font-size", "11px")
      .text("Hours");

    const tip: TooltipSelection = createTooltip(container);

    const rows = g
      .selectAll("g.row")
      .data(top)
      .join("g")
      .attr("class", "row")
      .attr("transform", (_d, i) => `translate(0,${y(i) ?? 0})`);

    rows
      .append("rect")
      .attr("x", 0)
      .attr("y", 0)
      .attr("height", y.bandwidth())
      .attr("width", (d) => x(d.totalSeconds))
      .attr("rx", 3)
      .attr("fill", (_d, i) => CHART_COLORS[i % CHART_COLORS.length])
      .on("mousemove", (event, d) => {
        showTooltip(
          tip,
          container,
          event,
          `<div style="font-weight:600">${d.name}</div>Activity: ${secondsToHms(
            d.totalSeconds,
          )}`,
        );
      })
      .on("mouseleave", () => hideTooltip(tip));

    // In-bar white labels (path basename), matching the Apex dataLabels.
    rows
      .append("text")
      .attr("x", 8)
      .attr("y", y.bandwidth() / 2)
      .attr("dominant-baseline", "central")
      .attr("fill", "#fff")
      .style("font-size", "11px")
      .style("pointer-events", "none")
      .text((d) => shortLabel(d.name));

    return () => {
      tip.remove();
    };
  }, [ref, top, height, frame.width, frame.themeKey]);

  if (top.length === 0) return <EmptyChart height={height} />;

  return (
    <div ref={ref} style={{ position: "relative", width: "100%", height }}>
      <svg ref={svgRef} />
    </div>
  );
}
