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
import type { RadarChartProps } from "@/components/charts/types";

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
  const { ref, frame } = useChartFrame(height);
  const svgRef = useRef<SVGSVGElement | null>(null);

  useEffect(() => {
    const svg = d3.select(svgRef.current);
    svg.selectAll("*").remove();
    const container = ref.current;
    if (!container || frame.width === 0) return;

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
    const width = frame.width;
    const cx = width / 2;
    const cy = height / 2;
    const radius = Math.min(width, height) / 2 - 34;
    const n = 7;
    const angle = (i: number) => (Math.PI * 2 * i) / n - Math.PI / 2;

    const maxVal = d3.max(values) ?? 0;
    const r = d3.scaleLinear().domain([0, maxVal || 1]).range([0, radius]);

    const g = svg
      .attr("width", width)
      .attr("height", height)
      .append("g")
      .attr("transform", `translate(${cx},${cy})`);

    // Concentric grid polygons.
    const levels = 4;
    for (let lvl = 1; lvl <= levels; lvl++) {
      const rr = (radius * lvl) / levels;
      const pts = d3
        .range(n)
        .map((i) => [Math.cos(angle(i)) * rr, Math.sin(angle(i)) * rr]);
      g.append("polygon")
        .attr("points", pts.map((p) => p.join(",")).join(" "))
        .attr("fill", "none")
        .attr("stroke", grid)
        .attr("stroke-opacity", gridOpacity);
    }

    // Spokes + axis labels.
    d3.range(n).forEach((i) => {
      const lx = Math.cos(angle(i)) * radius;
      const ly = Math.sin(angle(i)) * radius;
      g.append("line")
        .attr("x1", 0)
        .attr("y1", 0)
        .attr("x2", lx)
        .attr("y2", ly)
        .attr("stroke", grid)
        .attr("stroke-opacity", gridOpacity);
      g.append("text")
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
    const color = CHART_COLORS[0];
    g.append("polygon")
      .attr("points", dataPts.map((p) => p.join(",")).join(" "))
      .attr("fill", color)
      .attr("fill-opacity", 0.5)
      .attr("stroke", color)
      .attr("stroke-width", 2.5);

    const tip: TooltipSelection = createTooltip(container);

    g.selectAll("circle.pt")
      .data(values)
      .join("circle")
      .attr("class", "pt")
      .attr("cx", (_d, i) => Math.cos(angle(i)) * r(values[i]))
      .attr("cy", (_d, i) => Math.sin(angle(i)) * r(values[i]))
      .attr("r", 3)
      .attr("fill", color)
      .on("mousemove", (event, d) => {
        const i = values.indexOf(d);
        showTooltip(
          tip,
          container,
          event,
          `<div style="font-weight:600">${WEEKDAYS[i]}</div>Activity: ${secondsToHms(
            d,
          )}`,
        );
      })
      .on("mouseleave", () => hideTooltip(tip));

    return () => {
      tip.remove();
    };
  }, [ref, weekDay, height, frame.width, frame.themeKey]);

  return (
    <div ref={ref} style={{ position: "relative", width: "100%", height }}>
      <svg ref={svgRef} />
    </div>
  );
}
