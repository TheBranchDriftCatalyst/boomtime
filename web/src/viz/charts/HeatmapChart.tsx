import { useMemo } from "react";
import * as d3 from "d3";
import { secondsToHms, truncate } from "@/lib/utils";
import { cssVar } from "@/viz/d3/useChartFrame";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";
import { tooltipHtml } from "@/viz/d3/tooltip";
import { formatDay, styleAxis, thinnedDateTicks } from "@/viz/d3/axes";
import { colorAt, emptyFloor } from "@/viz/d3/color";
import { EmptyChart } from "@/viz/d3/EmptyChart";
import type { ResourceStats } from "@/types/api";

export interface HeatmapChartProps {
  // Top-N resources (projects or languages).
  items: ResourceStats[];
  dates: string[];
  topN?: number;
  height?: number;
}

const MARGIN = { top: 6, right: 8, bottom: 24, left: 96 };

/**
 * D3 activity heatmap where
 * each series (row) is shaded by its own value range against a base color drawn
 * from CHART_COLORS — low values near the card background, high values at the
 * full series color.
 */
export function HeatmapChart({
  items,
  dates,
  topN = 7,
  height = 260,
}: HeatmapChartProps) {
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

  const surface = useD3Surface(
    { height, margin: MARGIN },
    ({ g, innerW, innerH, showTip, hideTip }) => {
      if (rows.length === 0) return;

      const fg = cssVar("--muted-foreground");
      // Empty-cell floor: a subtle tone clearly above --card so the grid is
      // visible but 0-value cells read as "empty". A fixed rgb (not the oklch
      // --card token) so d3.interpolateRgb can parse both ends of the ramp.
      const floor = emptyFloor();

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
        const base = colorAt(i);
        const max = d3.max(r.totalDaily) ?? 0;
        const ramp = d3
          .scaleLinear<string>()
          .domain([0, max || 1])
          .range([floor, base])
          .interpolate(d3.interpolateRgb)
          .clamp(true);
        rowScale.set(r.name, (v: number) =>
          v <= 0 ? floor : ramp(Math.max(v, (max || 1) * 0.06)),
        );
      });

      // Y labels (truncated to 12 chars, full name on hover).
      styleAxis(
        g
          .append("g")
          .call(d3.axisLeft(y).tickSize(0).tickFormat((d) => truncate(String(d), 12))),
        { fg },
      )
        .selectAll<SVGTextElement, string>("text")
        .append("title")
        .text((d) => String(d));

      // X labels — thinned dates.
      styleAxis(
        g
          .append("g")
          .attr("transform", `translate(0,${innerH})`)
          .call(
            d3
              .axisBottom(x)
              .tickValues(thinnedDateTicks(dates))
              .tickFormat((d) => formatDay(new Date(d))),
          ),
        { fg },
        { fontSize: "10px" },
      );

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
          showTip(
            event,
            tooltipHtml(d.row.name, [
              formatDay(new Date(d.date)),
              secondsToHms(d.value),
            ]),
          );
        })
        .on("mouseleave", hideTip);
    },
    [rows, dates],
  );

  if (rows.length === 0 || dates.length === 0) {
    return <EmptyChart height={height} />;
  }

  return <ChartSurface surface={surface} />;
}
