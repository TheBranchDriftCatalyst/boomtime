import { useMemo } from "react";
import * as d3 from "d3";
import { secondsToHms } from "@/lib/utils";
import { cssVar } from "@/viz/d3/useChartFrame";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";
import { tooltipHtml } from "@/viz/d3/tooltip";
import { fmtPct, fmtRank } from "@/viz/d3/tooltipContent";
import { gridlines, hoursTickFormat } from "@/viz/d3/axes";
import { colorAt } from "@/viz/d3/color";
import { EmptyChart } from "@/viz/d3/EmptyChart";
import { shortPath } from "@/lib/pathLabel";
import type { ResourceStats } from "@/types/api";

export interface FileBarChartProps {
  files: ResourceStats[];
  height?: number;
  /** Total distinct files across the project (for "#N of files" rank context). */
  filesCount?: number;
}

const MARGIN = { top: 8, right: 16, bottom: 30, left: 8 };

/** D3 1:1 port of the most-active-files horizontal bar chart (top 10). */
export function FileBarChart({ files, height = 380, filesCount }: FileBarChartProps) {
  const top = useMemo(
    () =>
      [...files]
        .filter((v) => v.totalSeconds / 3600 > 0 && !v.name.startsWith("Other ("))
        .sort((a, b) => b.totalSeconds - a.totalSeconds)
        .slice(0, 10),
    [files],
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

      // X gridlines + hours axis at the bottom.
      gridlines(g, x, {
        orient: "bottom",
        span: innerH,
        offsetY: innerH,
        stroke: border,
        tickFormat: hoursTickFormat(),
        fg,
      });

      g.append("text")
        .attr("x", innerW)
        .attr("y", innerH + 26)
        .attr("text-anchor", "end")
        .attr("fill", fg)
        .style("font-size", "11px")
        .text("Hours");

      const rows = g
        .selectAll("g.row")
        .data(top)
        .join("g")
        .attr("class", "row")
        .attr("transform", (_d, i) => `translate(0,${y(i) ?? 0})`);

      // Total across ALL files (not just top-10) — the % is honest even when
      // there's a long tail below the chart.
      const grandTotal = d3.sum(files, (f) => f.totalSeconds) || 1;
      const totalFiles = filesCount ?? files.length;

      rows
        .append("rect")
        .attr("x", 0)
        .attr("y", 0)
        .attr("height", y.bandwidth())
        .attr("width", (d) => x(d.totalSeconds))
        .attr("rx", 3)
        .attr("fill", (_d, i) => colorAt(i))
        .on("mousemove", (event, d) => {
          const rank = top.indexOf(d) + 1;
          const share = (d.totalSeconds / grandTotal) * 100;
          const short = shortPath(d.name);
          showTip(
            event,
            tooltipHtml({
              title: short,
              titleSwatch: colorAt(rank - 1),
              subtitle: d.name !== short ? d.name : undefined,
              rows: [
                { label: "Time", value: secondsToHms(d.totalSeconds) },
                { label: "Share", value: fmtPct(share) },
              ],
              footer: fmtRank(rank, totalFiles),
            }),
          );
        })
        .on("mouseleave", hideTip);

      // Basename labels: inside the bar (white) when it fits, otherwise just
      // outside the bar end (foreground color) so short bars stay readable and
      // labels never spill unreadably over the plot background.
      rows
        .append("text")
        .attr("y", y.bandwidth() / 2)
        .attr("dominant-baseline", "central")
        .style("font-size", "11px")
        .style("pointer-events", "none")
        .each(function (d) {
          const label = shortPath(d.name);
          const barW = x(d.totalSeconds);
          const el = d3.select(this).text(label);
          const textW = (el.node() as SVGTextElement).getComputedTextLength();
          const fitsInside = barW >= textW + 16;
          if (fitsInside) {
            el.attr("x", 8).attr("text-anchor", "start").attr("fill", "#fff");
          } else {
            el.attr("x", barW + 6).attr("text-anchor", "start").attr("fill", fg);
          }
        });
    },
    [top, files, filesCount],
  );

  if (top.length === 0) return <EmptyChart height={height} />;

  return <ChartSurface surface={surface} />;
}
