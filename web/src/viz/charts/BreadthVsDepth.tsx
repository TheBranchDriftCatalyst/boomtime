import { useMemo } from "react";
import * as d3 from "d3";
import { secondsToHms } from "@/lib/utils";
import { cssVar } from "@/viz/d3/useChartFrame";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";
import { tooltipHtml } from "@/viz/d3/tooltip";
import { fmtDateRange } from "@/viz/d3/tooltipContent";
import {
  formatDay,
  hoursTickFormat,
  styleAxis,
  thinnedDateTicks,
} from "@/viz/d3/axes";
import { colorAt } from "@/viz/d3/color";
import { EmptyChart } from "@/viz/d3/EmptyChart";

interface BreadthVsDepthProps {
  // Bucketed parallel arrays.
  dates: string[];
  seconds: number[]; // time per bucket (depth)
  entities: number[]; // distinct files per bucket (breadth)
  height?: number;
  /** Optional per-bucket ranges — tooltips read as "12–18 Jan 2026". */
  ranges?: { start: string; end: string }[];
}

const TIME_COLOR = colorAt(0);
const FILES_COLOR = colorAt(4); // orange

const MARGIN = { top: 14, right: 48, bottom: 28, left: 48 };

/** Dual-axis: coding time (bars) vs distinct files touched (line). */
export function BreadthVsDepth({
  dates,
  seconds,
  entities,
  height = 300,
  ranges,
}: BreadthVsDepthProps) {
  const data = useMemo(
    () =>
      dates.map((d, i) => ({
        date: new Date(d),
        secs: seconds[i] ?? 0,
        files: entities[i] ?? 0,
        i,
      })),
    [dates, seconds, entities],
  );

  const hasData = entities.length > 0 && data.some((d) => d.secs > 0 || d.files > 0);

  const surface = useD3Surface(
    { height, margin: MARGIN },
    ({ g, innerW, innerH, showTip, hideTip }) => {
      if (!hasData) return;

      const fg = cssVar("--muted-foreground");
      const border = cssVar("--border");

      const x = d3
        .scaleBand<Date>()
        .domain(data.map((d) => d.date))
        .range([0, innerW])
        .padding(0.4);
      const yL = d3
        .scaleLinear()
        .domain([0, d3.max(data, (d) => d.secs) || 1])
        .nice()
        .range([innerH, 0]);
      const yR = d3
        .scaleLinear()
        .domain([0, d3.max(data, (d) => d.files) || 1])
        .nice()
        .range([innerH, 0]);

      // Left axis (hours).
      styleAxis(
        g.append("g").call(d3.axisLeft(yL).ticks(5).tickFormat(hoursTickFormat())),
        { fg: TIME_COLOR },
      );
      g.append("text")
        .attr("transform", "rotate(-90)")
        .attr("x", -innerH / 2)
        .attr("y", -MARGIN.left + 12)
        .attr("text-anchor", "middle")
        .attr("fill", TIME_COLOR)
        .style("font-size", "10px")
        .text("Hours");

      // Right axis (files).
      styleAxis(
        g
          .append("g")
          .attr("transform", `translate(${innerW},0)`)
          .call(d3.axisRight(yR).ticks(5).tickFormat((v) => String(v))),
        { fg: FILES_COLOR },
      );
      g.append("text")
        .attr("transform", "rotate(90)")
        .attr("x", innerH / 2)
        .attr("y", -innerW - MARGIN.right + 14)
        .attr("text-anchor", "middle")
        .attr("fill", FILES_COLOR)
        .style("font-size", "10px")
        .text("Files");

      styleAxis(
        g
          .append("g")
          .attr("transform", `translate(0,${innerH})`)
          .call(
            d3
              .axisBottom(x)
              .tickValues(thinnedDateTicks(data.map((d) => d.date)))
              .tickFormat((d) => formatDay(d as Date)),
          ),
        { fg, border },
        { domain: "line" },
      );

      // Time bars.
      g.selectAll("rect.bar")
        .data(data)
        .join("rect")
        .attr("class", "bar")
        .attr("x", (d) => x(d.date) ?? 0)
        .attr("width", x.bandwidth())
        .attr("y", (d) => yL(d.secs))
        .attr("height", (d) => innerH - yL(d.secs))
        .attr("rx", 3)
        .attr("fill", TIME_COLOR)
        .attr("fill-opacity", 0.85);

      // Files line.
      const line = d3
        .line<{ date: Date; files: number }>()
        .x((d) => (x(d.date) ?? 0) + x.bandwidth() / 2)
        .y((d) => yR(d.files))
        .curve(d3.curveMonotoneX);
      g.append("path")
        .datum(data)
        .attr("d", line)
        .attr("fill", "none")
        .attr("stroke", FILES_COLOR)
        .attr("stroke-width", 2);
      g.selectAll("circle.pt")
        .data(data)
        .join("circle")
        .attr("class", "pt")
        .attr("cx", (d) => (x(d.date) ?? 0) + x.bandwidth() / 2)
        .attr("cy", (d) => yR(d.files))
        .attr("r", 2.5)
        .attr("fill", FILES_COLOR);

      // Hover overlay per bucket.
      g.selectAll("rect.hit")
        .data(data)
        .join("rect")
        .attr("class", "hit")
        .attr("x", (d) => x(d.date) ?? 0)
        .attr("width", x.bandwidth())
        .attr("y", 0)
        .attr("height", innerH)
        .attr("fill", "transparent")
        .on("mousemove", (event, d) => {
          const rng = ranges?.[d.i];
          const subtitle =
            rng && rng.start && rng.end && rng.start !== rng.end
              ? fmtDateRange(rng.start, rng.end)
              : undefined;
          const rate = d.files > 0 ? d.secs / d.files : 0;
          showTip(
            event,
            tooltipHtml({
              title: d3.timeFormat("%d %b %Y")(d.date),
              subtitle,
              rows: [
                { label: "Time", value: secondsToHms(d.secs), swatch: TIME_COLOR },
                { label: "Files", value: String(d.files), swatch: FILES_COLOR },
                d.files > 0
                  ? {
                      label: "Time / file",
                      value: secondsToHms(Math.round(rate)),
                      muted: true,
                    }
                  : { label: "Time / file", value: "-", muted: true },
              ],
            }),
          );
        })
        .on("mouseleave", hideTip);
    },
    [data, hasData, ranges],
  );

  if (!hasData) return <EmptyChart height={height} />;

  return <ChartSurface surface={surface} />;
}
