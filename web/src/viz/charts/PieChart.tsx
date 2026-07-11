import { useMemo } from "react";
import * as d3 from "d3";
import { secondsToHms } from "@/lib/utils";
import { cssVar } from "@/viz/d3/useChartFrame";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";
import { tooltipHtml } from "@/viz/d3/tooltip";
import { MIN_SLICE_SECONDS, paletteByName } from "@/viz/d3/color";
import { EmptyChart } from "@/viz/d3/EmptyChart";
import type { ResourceStats } from "@/types/api";

export interface PieChartProps {
  items: ResourceStats[];
  height?: number;
}

/** D3 1:1 port of the breakdown pie chart (projects / languages). */
export function PieChart({ items, height = 320 }: PieChartProps) {
  const filtered = useMemo(
    () => items.filter((v) => v.totalSeconds >= MIN_SLICE_SECONDS),
    [items],
  );

  const surface = useD3Surface(
    { height },
    ({ g, width, showTip, hideTip }) => {
      if (filtered.length === 0) return;

      const card = cssVar("--card");
      const radius = Math.min(width, height) / 2 - 6;

      const pg = g
        .append("g")
        .attr("transform", `translate(${width / 2},${height / 2})`);

      const pie = d3
        .pie<ResourceStats>()
        .sort(null)
        .value((d) => d.totalSeconds);
      const arcs = pie(filtered);
      const total = d3.sum(filtered, (d) => d.totalSeconds) || 1;

      // Slice colors via the shared positional-palette contract (same filter
      // + order as `filtered`), so callers replaying the pie's palette
      // (Projects' stacked columns) can't desync from it.
      const palette = paletteByName(items, { minSeconds: MIN_SLICE_SECONDS });

      const arc = d3
        .arc<d3.PieArcDatum<ResourceStats>>()
        .innerRadius(0)
        .outerRadius(radius);
      const labelArc = d3
        .arc<d3.PieArcDatum<ResourceStats>>()
        .innerRadius(radius * 0.6)
        .outerRadius(radius * 0.6);

      pg.selectAll("path")
        .data(arcs)
        .join("path")
        .attr("d", arc)
        .attr("fill", (d) => palette.get(d.data.name)!)
        .attr("stroke", card)
        .attr("stroke-width", 1)
        .on("mousemove", (event, d) => {
          showTip(event, tooltipHtml(d.data.name, secondsToHms(d.data.totalSeconds)));
        })
        .on("mouseleave", hideTip);

      // Percentage labels for slices with enough room.
      pg.selectAll("text.slice")
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
    },
    [items, filtered],
  );

  if (filtered.length === 0) return <EmptyChart height={height} />;

  return <ChartSurface surface={surface} />;
}
