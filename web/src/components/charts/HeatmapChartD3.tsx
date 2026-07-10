import { useEffect, useMemo, useRef } from "react";
import * as d3 from "d3";
import { CHART_COLORS } from "@/lib/config";
import { secondsToHms, truncate } from "@/lib/utils";
import { cssVar, useChartFrame } from "@/viz/d3/useChartFrame";
import {
  createTooltip,
  hideTooltip,
  showTooltip,
  type TooltipSelection,
} from "@/viz/d3/tooltip";
import type { HeatmapChartProps } from "@/components/charts/types";
import type { ResourceStats } from "@/types/api";

/**
 * D3 1:1 port of the activity heatmap. Mirrors the ApexCharts heatmap where
 * each series (row) is shaded by its own value range against a base color drawn
 * from CHART_COLORS — low values near the card background, high values at the
 * full series color.
 */
export function HeatmapChartD3({
  items,
  dates,
  topN = 7,
  height = 260,
}: HeatmapChartProps) {
  const { ref, frame } = useChartFrame(height);
  const svgRef = useRef<SVGSVGElement | null>(null);

  const rows = useMemo(() => {
    const isOther = (r: ResourceStats) => r.name.startsWith("Other (");
    return [
      ...items
        .filter((r) => !isOther(r))
        .sort((a, b) => b.totalSeconds - a.totalSeconds)
        .slice(0, topN),
      ...items.filter(isOther),
    ];
  }, [items, topN]);

  useEffect(() => {
    const svg = d3.select(svgRef.current);
    svg.selectAll("*").remove();
    const container = ref.current;
    if (!container || frame.width === 0 || rows.length === 0) return;

    const fg = cssVar("--muted-foreground");
    const dark = document.documentElement.classList.contains("dark");
    // Empty-cell floor: a subtle tone clearly above --card so the grid is
    // visible but 0-value cells read as "empty". A fixed rgb (not the oklch
    // --card token) so d3.interpolateRgb can parse both ends of the ramp.
    const emptyFloor = dark ? "#232a36" : "#eceef2";
    const width = frame.width;
    const margin = { top: 6, right: 8, bottom: 24, left: 96 };
    const innerW = width - margin.left - margin.right;
    const innerH = height - margin.top - margin.bottom;

    const g = svg
      .attr("width", width)
      .attr("height", height)
      .append("g")
      .attr("transform", `translate(${margin.left},${margin.top})`);

    const x = d3
      .scaleBand<string>()
      .domain(dates)
      .range([0, innerW])
      .padding(0.08);
    const y = d3
      .scaleBand<string>()
      .domain(rows.map((r) => r.name))
      .range([0, innerH])
      .padding(0.12);

    // Per-row color scale by VALUE: empty floor → series base color at row max.
    // A tiny non-zero low anchor keeps the smallest active day just above the
    // empty floor so it's distinguishable, while 0 stays exactly on the floor.
    const rowScale = new Map<string, (v: number) => string>();
    rows.forEach((r, i) => {
      const base = CHART_COLORS[i % CHART_COLORS.length];
      const max = d3.max(r.totalDaily) ?? 0;
      const ramp = d3
        .scaleLinear<string>()
        .domain([0, max || 1])
        .range([emptyFloor, base])
        .interpolate(d3.interpolateRgb)
        .clamp(true);
      rowScale.set(r.name, (v: number) =>
        v <= 0 ? emptyFloor : ramp(Math.max(v, (max || 1) * 0.06)),
      );
    });

    // Y labels (truncated to 12 chars like the Apex formatter, full name on hover).
    g.append("g")
      .call(d3.axisLeft(y).tickSize(0).tickFormat((d) => truncate(String(d), 12)))
      .call((sel) => sel.select(".domain").remove())
      .selectAll<SVGTextElement, string>("text")
      .attr("fill", fg)
      .style("font-size", "11px")
      .append("title")
      .text((d) => String(d));

    // X labels — thinned dates.
    const tickEvery = Math.ceil(dates.length / 8) || 1;
    g.append("g")
      .attr("transform", `translate(0,${innerH})`)
      .call(
        d3
          .axisBottom(x)
          .tickValues(dates.filter((_, i) => i % tickEvery === 0))
          .tickFormat((d) => d3.timeFormat("%d %b")(new Date(d))),
      )
      .call((sel) => sel.select(".domain").remove())
      .selectAll("text")
      .attr("fill", fg)
      .style("font-size", "10px");

    const tip: TooltipSelection = createTooltip(container);

    const cells: { row: ResourceStats; date: string; value: number }[] = [];
    for (const row of rows) {
      dates.forEach((date, i) => {
        cells.push({ row, date, value: row.totalDaily[i] ?? 0 });
      });
    }

    g.selectAll("rect.cell")
      .data(cells)
      .join("rect")
      .attr("class", "cell")
      .attr("x", (d) => x(d.date) ?? 0)
      .attr("y", (d) => y(d.row.name) ?? 0)
      .attr("width", x.bandwidth())
      .attr("height", y.bandwidth())
      .attr("rx", 2)
      .attr("fill", (d) => rowScale.get(d.row.name)!(d.value))
      .on("mousemove", (event, d) => {
        showTooltip(
          tip,
          container,
          event,
          `<div style="font-weight:600">${d.row.name}</div>` +
            `${d3.timeFormat("%d %b")(new Date(d.date))}: ${secondsToHms(d.value)}`,
        );
      })
      .on("mouseleave", () => hideTooltip(tip));

    return () => {
      tip.remove();
    };
  }, [ref, rows, dates, height, frame.width, frame.themeKey]);

  return (
    <div ref={ref} style={{ position: "relative", width: "100%", height }}>
      <svg ref={svgRef} />
    </div>
  );
}
