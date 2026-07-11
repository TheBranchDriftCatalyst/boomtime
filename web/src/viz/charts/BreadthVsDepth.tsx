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

interface BreadthVsDepthProps {
  // Bucketed parallel arrays.
  dates: string[];
  seconds: number[]; // time per bucket (depth)
  entities: number[]; // distinct files per bucket (breadth)
  height?: number;
}

const TIME_COLOR = CHART_COLORS[0];
const FILES_COLOR = CHART_COLORS[4]; // orange

/** Dual-axis: coding time (bars) vs distinct files touched (line). */
export function BreadthVsDepth({
  dates,
  seconds,
  entities,
  height = 300,
}: BreadthVsDepthProps) {
  const { ref, frame } = useChartFrame(height);
  const svgRef = useRef<SVGSVGElement | null>(null);

  const data = useMemo(
    () =>
      dates.map((d, i) => ({
        date: new Date(d),
        secs: seconds[i] ?? 0,
        files: entities[i] ?? 0,
      })),
    [dates, seconds, entities],
  );

  const hasData = entities.length > 0 && data.some((d) => d.secs > 0 || d.files > 0);

  useEffect(() => {
    const svg = d3.select(svgRef.current);
    svg.selectAll("*").remove();
    const container = ref.current;
    if (!container || frame.width === 0 || !hasData) return;

    const fg = cssVar("--muted-foreground");
    const border = cssVar("--border");
    const width = frame.width;
    const margin = { top: 14, right: 48, bottom: 28, left: 48 };
    const innerW = width - margin.left - margin.right;
    const innerH = height - margin.top - margin.bottom;

    const g = svg
      .attr("width", width)
      .attr("height", height)
      .append("g")
      .attr("transform", `translate(${margin.left},${margin.top})`);

    const x = d3.scaleBand<Date>().domain(data.map((d) => d.date)).range([0, innerW]).padding(0.4);
    const yL = d3.scaleLinear().domain([0, d3.max(data, (d) => d.secs) || 1]).nice().range([innerH, 0]);
    const yR = d3.scaleLinear().domain([0, d3.max(data, (d) => d.files) || 1]).nice().range([innerH, 0]);

    // Left axis (hours).
    g.append("g")
      .call(d3.axisLeft(yL).ticks(5).tickFormat((v) => `${(Number(v) / 3600).toFixed(1)}`))
      .call((sel) => sel.select(".domain").remove())
      .selectAll("text")
      .attr("fill", TIME_COLOR)
      .style("font-size", "11px");
    g.append("text")
      .attr("transform", "rotate(-90)")
      .attr("x", -innerH / 2)
      .attr("y", -margin.left + 12)
      .attr("text-anchor", "middle")
      .attr("fill", TIME_COLOR)
      .style("font-size", "10px")
      .text("Hours");

    // Right axis (files).
    g.append("g")
      .attr("transform", `translate(${innerW},0)`)
      .call(d3.axisRight(yR).ticks(5).tickFormat((v) => String(v)))
      .call((sel) => sel.select(".domain").remove())
      .selectAll("text")
      .attr("fill", FILES_COLOR)
      .style("font-size", "11px");
    g.append("text")
      .attr("transform", "rotate(90)")
      .attr("x", innerH / 2)
      .attr("y", -innerW - margin.right + 14)
      .attr("text-anchor", "middle")
      .attr("fill", FILES_COLOR)
      .style("font-size", "10px")
      .text("Files");

    const tickEvery = Math.ceil(data.length / 8) || 1;
    g.append("g")
      .attr("transform", `translate(0,${innerH})`)
      .call(
        d3
          .axisBottom(x)
          .tickValues(data.map((d) => d.date).filter((_, i) => i % tickEvery === 0))
          .tickFormat((d) => d3.timeFormat("%d %b")(d as Date)),
      )
      .call((sel) => sel.select(".domain").attr("stroke", border))
      .selectAll("text")
      .attr("fill", fg)
      .style("font-size", "11px");

    const tip: TooltipSelection = createTooltip(container);

    // Time bars.
    g.selectAll("rect.bar")
      .data(data)
      .join("rect")
      .attr("class", "bar")
      .attr("x", (d) => x(d.date) ?? 0)
      .attr("width", x.bandwidth())
      .attr("y", (d) => yL(d.secs))
      .attr("height", (d) => innerH - yL(d.secs))
      .attr("rx", 3)
      .attr("fill", TIME_COLOR)
      .attr("fill-opacity", 0.85);

    // Files line.
    const line = d3
      .line<{ date: Date; files: number }>()
      .x((d) => (x(d.date) ?? 0) + x.bandwidth() / 2)
      .y((d) => yR(d.files))
      .curve(d3.curveMonotoneX);
    g.append("path")
      .datum(data)
      .attr("d", line)
      .attr("fill", "none")
      .attr("stroke", FILES_COLOR)
      .attr("stroke-width", 2);
    g.selectAll("circle.pt")
      .data(data)
      .join("circle")
      .attr("class", "pt")
      .attr("cx", (d) => (x(d.date) ?? 0) + x.bandwidth() / 2)
      .attr("cy", (d) => yR(d.files))
      .attr("r", 2.5)
      .attr("fill", FILES_COLOR);

    // Hover overlay per bucket.
    g.selectAll("rect.hit")
      .data(data)
      .join("rect")
      .attr("class", "hit")
      .attr("x", (d) => x(d.date) ?? 0)
      .attr("width", x.bandwidth())
      .attr("y", 0)
      .attr("height", innerH)
      .attr("fill", "transparent")
      .on("mousemove", (event, d) => {
        showTooltip(
          tip,
          container,
          event,
          `<div style="font-weight:600">${d3.timeFormat("%d %b %Y")(d.date)}</div>` +
            `Time: ${secondsToHms(d.secs)}<br/>Files: ${d.files}`,
        );
      })
      .on("mouseleave", () => hideTooltip(tip));

    return () => {
      tip.remove();
    };
  }, [data, hasData, height, frame.width, frame.themeKey, ref]);

  if (!hasData) return <EmptyChart height={height} />;

  return (
    <div ref={ref} style={{ position: "relative", width: "100%", height }}>
      <svg ref={svgRef} />
    </div>
  );
}
