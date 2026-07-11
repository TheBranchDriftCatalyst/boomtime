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

interface CategoryStreamgraphProps {
  // Category resources with a per-bucket `totalDaily` series (already bucketed
  // by the caller) aligned to `dates`.
  categories: ResourceStats[];
  dates: string[];
  height?: number;
}

/**
 * Streamgraph (stacked area with wiggle/silhouette offset) of category time
 * over the bucketed day series — the "what kind of work" narrative. Legend +
 * hover. Dark-mode native; responsive.
 */
export function CategoryStreamgraph({
  categories,
  dates,
  height = 320,
}: CategoryStreamgraphProps) {
  const { ref, frame } = useChartFrame(height);
  const svgRef = useRef<SVGSVGElement | null>(null);

  // Keep the aggregated "Other (…)" bucket last, real categories by total desc.
  const series = useMemo(() => {
    const isOther = (r: ResourceStats) => r.name.startsWith("Other (");
    return [
      ...categories.filter((c) => !isOther(c)).sort((a, b) => b.totalSeconds - a.totalSeconds),
      ...categories.filter(isOther),
    ].filter((c) => c.totalSeconds > 0);
  }, [categories]);

  useEffect(() => {
    const svg = d3.select(svgRef.current);
    svg.selectAll("*").remove();
    const container = ref.current;
    if (!container || frame.width === 0 || series.length === 0 || dates.length === 0)
      return;

    const fg = cssVar("--muted-foreground");
    const card = cssVar("--card");
    const width = frame.width;
    const legendH = 22;
    const margin = { top: legendH + 4, right: 12, bottom: 24, left: 12 };
    const innerW = width - margin.left - margin.right;
    const innerH = height - margin.top - margin.bottom;

    const g = svg
      .attr("width", width)
      .attr("height", height)
      .append("g")
      .attr("transform", `translate(${margin.left},${margin.top})`);

    // Build one row per bucket: { i, <cat>: seconds }.
    const keys = series.map((s) => s.name);
    const rows = dates.map((_d, i) => {
      const row: Record<string, number> = { i };
      for (const s of series) row[s.name] = s.totalDaily[i] ?? 0;
      return row;
    });

    const stack = d3
      .stack<Record<string, number>>()
      .keys(keys)
      .offset(d3.stackOffsetWiggle)
      .order(d3.stackOrderInsideOut);
    const stacked = stack(rows);

    const x = d3
      .scaleLinear()
      .domain([0, dates.length - 1])
      .range([0, innerW]);
    const yMin = d3.min(stacked, (layer) => d3.min(layer, (d) => d[0])) ?? 0;
    const yMax = d3.max(stacked, (layer) => d3.max(layer, (d) => d[1])) ?? 1;
    const y = d3.scaleLinear().domain([yMin, yMax]).range([innerH, 0]);
    const color = (i: number) => CHART_COLORS[i % CHART_COLORS.length];

    const area = d3
      .area<d3.SeriesPoint<Record<string, number>>>()
      .x((d) => x(d.data.i))
      .y0((d) => y(d[0]))
      .y1((d) => y(d[1]))
      .curve(d3.curveBasis);

    const tip: TooltipSelection = createTooltip(container);

    g.selectAll("path.layer")
      .data(stacked)
      .join("path")
      .attr("class", "layer")
      .attr("d", area)
      .attr("fill", (_layer, i) => color(i))
      .attr("stroke", card)
      .attr("stroke-width", 0.5)
      .on("mousemove", (event, layer) => {
        const total = series.find((s) => s.name === layer.key)?.totalSeconds ?? 0;
        showTooltip(
          tip,
          container,
          event,
          `<div style="font-weight:600">${layer.key}</div>${secondsToHms(total)}`,
        );
      })
      .on("mouseleave", () => hideTooltip(tip));

    // X axis (dates, thinned).
    const tickEvery = Math.ceil(dates.length / 8) || 1;
    g.append("g")
      .attr("transform", `translate(0,${innerH})`)
      .call(
        d3
          .axisBottom(x)
          .tickValues(d3.range(dates.length).filter((i) => i % tickEvery === 0))
          .tickFormat((i) => d3.timeFormat("%d %b")(new Date(dates[Number(i)]))),
      )
      .call((sel) => sel.select(".domain").remove())
      .selectAll("text")
      .attr("fill", fg)
      .style("font-size", "10px");

    // Legend across the top.
    const legend = svg.append("g").attr("transform", `translate(${margin.left},2)`);
    let offset = 0;
    keys.forEach((k, i) => {
      const item = legend.append("g").attr("transform", `translate(${offset},0)`);
      item
        .append("rect")
        .attr("width", 10)
        .attr("height", 10)
        .attr("rx", 2)
        .attr("y", 3)
        .attr("fill", color(i));
      const label = item
        .append("text")
        .attr("x", 14)
        .attr("y", 12)
        .attr("fill", fg)
        .style("font-size", "11px")
        .text(k);
      offset += (label.node()?.getComputedTextLength() ?? 40) + 28;
    });

    return () => {
      tip.remove();
    };
  }, [series, dates, height, frame.width, frame.themeKey, ref]);

  if (series.length === 0) return <EmptyChart height={height} />;

  return (
    <div ref={ref} style={{ position: "relative", width: "100%", height }}>
      <svg ref={svgRef} />
    </div>
  );
}
