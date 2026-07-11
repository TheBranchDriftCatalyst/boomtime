import * as d3 from "d3";
import { secondsToHms } from "@/lib/utils";
import { cssVar } from "@/viz/d3/useChartFrame";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";
import { tooltipHtml } from "@/viz/d3/tooltip";
import { fmtPct, fmtRank } from "@/viz/d3/tooltipContent";
import { colorAt } from "@/viz/d3/color";
import { EmptyChart } from "@/viz/d3/EmptyChart";
import type { ResourceStats } from "@/types/api";

export interface RadarChartProps {
  // weekDay resources: name is the weekday index (0-6).
  weekDay: ResourceStats[];
  height?: number;
}

const WEEKDAYS = [
  "Sunday",
  "Monday",
  "Tuesday",
  "Wednesday",
  "Thursday",
  "Friday",
  "Saturday",
];

/** D3 1:1 port of the activity-per-weekday radar chart. */
export function RadarChart({ weekDay, height = 320 }: RadarChartProps) {
  const surface = useD3Surface(
    { height },
    ({ g, width, showTip, hideTip }) => {
      const values = Array(7).fill(0) as number[];
      for (const v of weekDay) {
        const idx = parseInt(v.name, 10);
        if (!Number.isNaN(idx) && idx >= 0 && idx < 7) values[idx] = v.totalSeconds;
      }

      const fg = cssVar("--muted-foreground");
      // The --border token is nearly invisible on dark; use muted-foreground at a
      // low opacity for legible-but-subtle grid rings and spokes.
      const grid = fg;
      const gridOpacity = 0.35;
      const cx = width / 2;
      const cy = height / 2;
      const radius = Math.min(width, height) / 2 - 34;
      const n = 7;
      const angle = (i: number) => (Math.PI * 2 * i) / n - Math.PI / 2;

      const maxVal = d3.max(values) ?? 0;
      const r = d3.scaleLinear().domain([0, maxVal || 1]).range([0, radius]);

      const rg = g.append("g").attr("transform", `translate(${cx},${cy})`);

      // Concentric grid polygons.
      const levels = 4;
      for (let lvl = 1; lvl <= levels; lvl++) {
        const rr = (radius * lvl) / levels;
        const pts = d3
          .range(n)
          .map((i) => [Math.cos(angle(i)) * rr, Math.sin(angle(i)) * rr]);
        rg.append("polygon")
          .attr("points", pts.map((p) => p.join(",")).join(" "))
          .attr("fill", "none")
          .attr("stroke", grid)
          .attr("stroke-opacity", gridOpacity);
      }

      // Spokes + axis labels.
      d3.range(n).forEach((i) => {
        const lx = Math.cos(angle(i)) * radius;
        const ly = Math.sin(angle(i)) * radius;
        rg.append("line")
          .attr("x1", 0)
          .attr("y1", 0)
          .attr("x2", lx)
          .attr("y2", ly)
          .attr("stroke", grid)
          .attr("stroke-opacity", gridOpacity);
        rg.append("text")
          .attr("x", Math.cos(angle(i)) * (radius + 18))
          .attr("y", Math.sin(angle(i)) * (radius + 18))
          .attr("text-anchor", "middle")
          .attr("dominant-baseline", "central")
          .attr("fill", fg)
          .style("font-size", "11px")
          .style("font-weight", "500")
          .text(WEEKDAYS[i].slice(0, 3));
      });

      // Data polygon.
      const dataPts = values.map((v, i) => [
        Math.cos(angle(i)) * r(v),
        Math.sin(angle(i)) * r(v),
      ]);
      const color = colorAt(0);
      rg.append("polygon")
        .attr("points", dataPts.map((p) => p.join(",")).join(" "))
        .attr("fill", color)
        .attr("fill-opacity", 0.5)
        .attr("stroke", color)
        .attr("stroke-width", 2.5);

      // Bind {i, value} objects (not raw numbers): duplicate values (e.g. two
      // zero days) would otherwise make indexOf resolve the wrong weekday.
      // Total across all weekdays + per-weekday rank (by seconds desc).
      const total = d3.sum(values);
      const rank = new Map<number, number>();
      values
        .map((v, i) => ({ i, v }))
        .filter((d) => d.v > 0)
        .sort((a, b) => b.v - a.v)
        .forEach((d, k) => rank.set(d.i, k + 1));
      const activeDays = values.filter((v) => v > 0).length;

      rg.selectAll("circle.pt")
        .data(values.map((value, i) => ({ i, value })))
        .join("circle")
        .attr("class", "pt")
        .attr("cx", (d) => Math.cos(angle(d.i)) * r(d.value))
        .attr("cy", (d) => Math.sin(angle(d.i)) * r(d.value))
        .attr("r", 3)
        .attr("fill", color)
        .on("mousemove", (event, d) => {
          const share = total > 0 ? (d.value / total) * 100 : 0;
          const r = rank.get(d.i);
          showTip(
            event,
            tooltipHtml({
              title: WEEKDAYS[d.i],
              titleSwatch: color,
              rows:
                d.value > 0
                  ? [
                      { label: "Activity", value: secondsToHms(d.value) },
                      { label: "Share of week", value: fmtPct(share) },
                    ]
                  : [{ label: "Activity", value: "0" }],
              footer: r ? fmtRank(r, activeDays) : undefined,
            }),
          );
        })
        .on("mouseleave", hideTip);
    },
    [weekDay],
  );

  if (weekDay.length === 0) return <EmptyChart height={height} />;

  return <ChartSurface surface={surface} />;
}
