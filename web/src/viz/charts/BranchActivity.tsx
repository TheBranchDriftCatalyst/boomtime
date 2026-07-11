import { useMemo } from "react";
import * as d3 from "d3";
import { secondsToHms } from "@/lib/utils";
import { cssVar } from "@/viz/d3/useChartFrame";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";
import { tooltipHtml } from "@/viz/d3/tooltip";
import { gridlines, hoursTickFormat } from "@/viz/d3/axes";
import { colorAt } from "@/viz/d3/color";
import { EmptyChart } from "@/viz/d3/EmptyChart";
import type { ResourceStats } from "@/types/api";

interface BranchActivityProps {
  branches: ResourceStats[] | undefined;
  height?: number;
}

const MARGIN = { top: 8, right: 16, bottom: 28, left: 8 };

/** Horizontal bar of time per git branch (top-N, aggregate "Other" excluded). */
export function BranchActivity({ branches, height = 320 }: BranchActivityProps) {
  const top = useMemo(
    () =>
      [...(branches ?? [])]
        // The "Other (N more)" aggregate reads oddly in a per-branch bar; drop it.
        .filter((b) => b.totalSeconds > 0 && !b.name.startsWith("Other ("))
        .sort((a, b) => b.totalSeconds - a.totalSeconds)
        .slice(0, 12),
    [branches],
  );

  const surface = useD3Surface(
    { height, margin: MARGIN },
    ({ g, innerW, innerH, showTip, hideTip }) => {
      if (top.length === 0) return;

      const fg = cssVar("--muted-foreground");
      const border = cssVar("--border");

      const y = d3
        .scaleBand<number>()
        .domain(d3.range(top.length))
        .range([0, innerH])
        .padding(0.2);
      const xMax = d3.max(top, (d) => d.totalSeconds) ?? 0;
      const x = d3.scaleLinear().domain([0, xMax || 1]).nice().range([0, innerW]);

      gridlines(g, x, {
        orient: "bottom",
        span: innerH,
        offsetY: innerH,
        stroke: border,
        tickFormat: hoursTickFormat({ suffix: "h" }),
        fg,
      });

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
        .attr("fill", (_d, i) => colorAt(i))
        .on("mousemove", (event, d) => {
          showTip(event, tooltipHtml(d.name, secondsToHms(d.totalSeconds)));
        })
        .on("mouseleave", hideTip);

      rows
        .append("text")
        .attr("x", 8)
        .attr("y", y.bandwidth() / 2)
        .attr("dominant-baseline", "central")
        .attr("fill", "#fff")
        .style("font-size", "11px")
        .style("pointer-events", "none")
        .text((d) => d.name);
    },
    [top],
  );

  if (top.length === 0) return <EmptyChart height={height} />;

  return <ChartSurface surface={surface} />;
}
