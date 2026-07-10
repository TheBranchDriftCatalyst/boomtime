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
import type { ColumnChartProps } from "@/components/charts/types";

/** D3 1:1 port of the daily-activity column chart. */
export function ColumnChartD3({
  dates,
  values,
  seriesName = "Coding time",
  height = 320,
}: ColumnChartProps) {
  const { ref, frame } = useChartFrame(height);
  const svgRef = useRef<SVGSVGElement | null>(null);

  useEffect(() => {
    const svg = d3.select(svgRef.current);
    svg.selectAll("*").remove();
    const container = ref.current;
    if (!container || frame.width === 0) return;

    const data = dates.map((d, i) => ({ date: new Date(d), y: values[i] ?? 0 }));

    const fg = cssVar("--muted-foreground");
    const border = cssVar("--border");
    const width = frame.width;
    const margin = { top: 10, right: 12, bottom: 28, left: 44 };
    const innerW = width - margin.left - margin.right;
    const innerH = height - margin.top - margin.bottom;

    const g = svg
      .attr("width", width)
      .attr("height", height)
      .append("g")
      .attr("transform", `translate(${margin.left},${margin.top})`);

    const x = d3
      .scaleBand<Date>()
      .domain(data.map((d) => d.date))
      .range([0, innerW])
      .padding(0.55);

    const yMax = d3.max(data, (d) => d.y) ?? 0;
    const y = d3.scaleLinear().domain([0, yMax || 1]).nice().range([innerH, 0]);

    // Gridlines (dashed, matching the Apex grid).
    g.append("g")
      .attr("color", border)
      .call(
        d3
          .axisLeft(y)
          .ticks(5)
          .tickSize(-innerW)
          .tickFormat(() => ""),
      )
      .call((sel) => sel.select(".domain").remove())
      .selectAll("line")
      .attr("stroke", border)
      .attr("stroke-dasharray", "4");

    // Y axis (hours).
    g.append("g")
      .call(
        d3
          .axisLeft(y)
          .ticks(5)
          .tickFormat((v) => (Number(v) / 3600).toFixed(1)),
      )
      .call((sel) => sel.select(".domain").remove())
      .selectAll("text")
      .attr("fill", fg)
      .style("font-size", "11px");

    // Y axis title.
    g.append("text")
      .attr("transform", "rotate(-90)")
      .attr("x", -innerH / 2)
      .attr("y", -margin.left + 12)
      .attr("text-anchor", "middle")
      .attr("fill", fg)
      .style("font-size", "11px")
      .text("Hours");

    // X axis (dates) — thin out ticks to avoid clutter.
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

    g.selectAll("rect.bar")
      .data(data)
      .join("rect")
      .attr("class", "bar")
      .attr("x", (d) => x(d.date) ?? 0)
      .attr("width", x.bandwidth())
      .attr("y", (d) => y(d.y))
      .attr("height", (d) => innerH - y(d.y))
      .attr("rx", 4)
      .attr("fill", CHART_COLORS[0])
      .on("mousemove", (event, d) => {
        showTooltip(
          tip,
          container,
          event,
          `<div style="font-weight:600">${d3.timeFormat("%d %b")(d.date)}</div>` +
            `${seriesName}: ${secondsToHms(d.y)}`,
        );
      })
      .on("mouseleave", () => hideTooltip(tip));

    return () => {
      tip.remove();
    };
  }, [ref, dates, values, seriesName, height, frame.width, frame.themeKey]);

  return (
    <div ref={ref} style={{ position: "relative", width: "100%", height }}>
      <svg ref={svgRef} />
    </div>
  );
}
