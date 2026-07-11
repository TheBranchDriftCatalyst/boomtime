import { useEffect, useMemo, useRef } from "react";
import * as d3 from "d3";
import { secondsToHms, truncate } from "@/lib/utils";
import { cssVar, useChartFrame } from "@/viz/d3/useChartFrame";
import {
  createTooltip,
  hideTooltip,
  showTooltip,
  type TooltipSelection,
} from "@/viz/d3/tooltip";
import { EmptyChart } from "@/viz/d3/EmptyChart";
import type { MomentumPayload } from "@/types/api";

interface MomentumGridProps {
  data: MomentumPayload | undefined;
  rowHeight?: number;
}

/**
 * Project x week momentum heatmap: rows = top projects, cols = weeks, cell
 * intensity ∝ seconds (per-row scale so each project's ramp shows). Reveals
 * which projects are heating up / cooling down. Row label + hover.
 */
export function MomentumGrid({ data, rowHeight = 26 }: MomentumGridProps) {
  const rows = useMemo(() => data?.projects ?? [], [data]);
  const weeks = useMemo(() => data?.weeks ?? [], [data]);
  const height = Math.max(120, rows.length * rowHeight + 40);

  const { ref, frame } = useChartFrame(height);
  const svgRef = useRef<SVGSVGElement | null>(null);

  useEffect(() => {
    const svg = d3.select(svgRef.current);
    svg.selectAll("*").remove();
    const container = ref.current;
    if (!container || frame.width === 0 || rows.length === 0 || weeks.length === 0)
      return;

    const fg = cssVar("--muted-foreground");
    const base = cssVar("--primary");
    const dark = document.documentElement.classList.contains("dark");
    const emptyFloor = dark ? "#232a36" : "#eceef2";
    const width = frame.width;
    const margin = { top: 6, right: 8, bottom: 26, left: 110 };
    const innerW = width - margin.left - margin.right;
    const innerH = height - margin.top - margin.bottom;

    const g = svg
      .attr("width", width)
      .attr("height", height)
      .append("g")
      .attr("transform", `translate(${margin.left},${margin.top})`);

    const x = d3.scaleBand<number>().domain(d3.range(weeks.length)).range([0, innerW]).padding(0.08);
    const y = d3.scaleBand<string>().domain(rows.map((r) => r.name)).range([0, innerH]).padding(0.12);

    // Per-row opacity ramp over --primary so even small weeks are visible (0 =>
    // empty floor; smallest active => ~0.2; row max => 1.0). Using opacity of a
    // solid primary fill avoids interpolating the oklch theme tokens (which
    // d3.interpolateRgb can't parse and would collapse to black).
    const rowOpacity = new Map<string, (v: number) => number>();
    for (const p of rows) {
      const max = d3.max(p.weekly) ?? 0;
      const scale = d3
        .scaleLinear()
        .domain([0, max || 1])
        .range([0.2, 1])
        .clamp(true);
      rowOpacity.set(p.name, (v: number) => (v <= 0 ? 0 : scale(v)));
    }

    // Row labels (project names, truncated; full name on hover).
    g.append("g")
      .call(d3.axisLeft(y).tickSize(0).tickFormat((d) => truncate(String(d), 14)))
      .call((sel) => sel.select(".domain").remove())
      .selectAll<SVGTextElement, string>("text")
      .attr("fill", fg)
      .style("font-size", "11px")
      .append("title")
      .text((d) => String(d));

    // Week axis (thinned to ~8 labels).
    const tickEvery = Math.ceil(weeks.length / 8) || 1;
    g.append("g")
      .attr("transform", `translate(0,${innerH})`)
      .call(
        d3
          .axisBottom(x)
          .tickValues(d3.range(weeks.length).filter((i) => i % tickEvery === 0))
          .tickFormat((i) => d3.timeFormat("%d %b")(new Date(weeks[Number(i)]))),
      )
      .call((sel) => sel.select(".domain").remove())
      .selectAll("text")
      .attr("fill", fg)
      .style("font-size", "10px");

    const tip: TooltipSelection = createTooltip(container);

    const cells: { project: string; wi: number; seconds: number }[] = [];
    for (const p of rows) {
      weeks.forEach((_w, wi) => {
        cells.push({ project: p.name, wi, seconds: p.weekly[wi] ?? 0 });
      });
    }

    // Empty-floor background so the grid reads even where activity is 0.
    g.selectAll("rect.floor")
      .data(cells)
      .join("rect")
      .attr("class", "floor")
      .attr("x", (c) => x(c.wi) ?? 0)
      .attr("y", (c) => y(c.project) ?? 0)
      .attr("width", x.bandwidth())
      .attr("height", y.bandwidth())
      .attr("rx", 2)
      .attr("fill", emptyFloor);

    g.selectAll("rect.cell")
      .data(cells)
      .join("rect")
      .attr("class", "cell")
      .attr("x", (c) => x(c.wi) ?? 0)
      .attr("y", (c) => y(c.project) ?? 0)
      .attr("width", x.bandwidth())
      .attr("height", y.bandwidth())
      .attr("rx", 2)
      .attr("fill", base)
      .attr("fill-opacity", (c) => rowOpacity.get(c.project)!(c.seconds))
      .on("mousemove", (event, c) => {
        showTooltip(
          tip,
          container,
          event,
          `<div style="font-weight:600">${c.project}</div>` +
            `${d3.timeFormat("%d %b %Y")(new Date(weeks[c.wi]))}: ${secondsToHms(c.seconds)}`,
        );
      })
      .on("mouseleave", () => hideTooltip(tip));

    return () => {
      tip.remove();
    };
  }, [rows, weeks, height, frame.width, frame.themeKey, ref]);

  if (rows.length === 0 || weeks.length === 0) return <EmptyChart height={140} />;

  return (
    <div ref={ref} style={{ position: "relative", width: "100%", height }}>
      <svg ref={svgRef} />
    </div>
  );
}
