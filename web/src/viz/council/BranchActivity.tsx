import { useEffect, useMemo, useRef } from "react";
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
import type { ResourceStats } from "@/types/api";

interface BranchActivityProps {
  branches: ResourceStats[] | undefined;
  height?: number;
}

/** Horizontal bar of time per git branch (top-N, aggregate "Other" excluded). */
export function BranchActivity({ branches, height = 320 }: BranchActivityProps) {
  const { ref, frame } = useChartFrame(height);
  const svgRef = useRef<SVGSVGElement | null>(null);

  const top = useMemo(
    () =>
      [...(branches ?? [])]
        // The "Other (N more)" aggregate reads oddly in a per-branch bar; drop it.
        .filter((b) => b.totalSeconds > 0 && !b.name.startsWith("Other ("))
        .sort((a, b) => b.totalSeconds - a.totalSeconds)
        .slice(0, 12),
    [branches],
  );

  useEffect(() => {
    const svg = d3.select(svgRef.current);
    svg.selectAll("*").remove();
    const container = ref.current;
    if (!container || frame.width === 0 || top.length === 0) return;

    const fg = cssVar("--muted-foreground");
    const border = cssVar("--border");
    const width = frame.width;
    const margin = { top: 8, right: 16, bottom: 28, left: 8 };
    const innerW = width - margin.left - margin.right;
    const innerH = height - margin.top - margin.bottom;

    const g = svg
      .attr("width", width)
      .attr("height", height)
      .append("g")
      .attr("transform", `translate(${margin.left},${margin.top})`);

    const y = d3.scaleBand<number>().domain(d3.range(top.length)).range([0, innerH]).padding(0.2);
    const xMax = d3.max(top, (d) => d.totalSeconds) ?? 0;
    const x = d3.scaleLinear().domain([0, xMax || 1]).nice().range([0, innerW]);

    g.append("g")
      .attr("transform", `translate(0,${innerH})`)
      .call(
        d3.axisBottom(x).ticks(5).tickSize(-innerH).tickFormat((v) => `${(Number(v) / 3600).toFixed(1)}h`),
      )
      .call((sel) => sel.select(".domain").remove())
      .call((sel) => sel.selectAll("line").attr("stroke", border).attr("stroke-dasharray", "4"))
      .selectAll("text")
      .attr("fill", fg)
      .style("font-size", "11px");

    const tip: TooltipSelection = createTooltip(container);

    const rows = g
      .selectAll("g.row")
      .data(top)
      .join("g")
      .attr("class", "row")
      .attr("transform", (_d, i) => `translate(0,${y(i) ?? 0})`);

    rows
      .append("rect")
      .attr("height", y.bandwidth())
      .attr("width", (d) => x(d.totalSeconds))
      .attr("rx", 3)
      .attr("fill", (_d, i) => CHART_COLORS[i % CHART_COLORS.length])
      .on("mousemove", (event, d) => {
        showTooltip(
          tip,
          container,
          event,
          `<div style="font-weight:600">${d.name}</div>${secondsToHms(d.totalSeconds)}`,
        );
      })
      .on("mouseleave", () => hideTooltip(tip));

    rows
      .append("text")
      .attr("x", 8)
      .attr("y", y.bandwidth() / 2)
      .attr("dominant-baseline", "central")
      .attr("fill", "#fff")
      .style("font-size", "11px")
      .style("pointer-events", "none")
      .text((d) => d.name);

    return () => {
      tip.remove();
    };
  }, [top, height, frame.width, frame.themeKey, ref]);

  if (top.length === 0) return <EmptyChart height={height} />;

  return (
    <div ref={ref} style={{ position: "relative", width: "100%", height }}>
      <svg ref={svgRef} />
    </div>
  );
}
