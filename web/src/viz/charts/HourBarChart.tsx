import * as d3 from "d3";
import { addTimeOffset, secondsToHms } from "@/lib/utils";
import { cssVar } from "@/viz/d3/useChartFrame";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";
import { tooltipHtml } from "@/viz/d3/tooltip";
import { gridlines, hoursTickFormat, styleAxis } from "@/viz/d3/axes";
import { colorAt } from "@/viz/d3/color";
import { EmptyChart } from "@/viz/d3/EmptyChart";
import type { ResourceStats } from "@/types/api";

export interface HourBarChartProps {
  // hour resources: name is the hour-of-day (0-23) in UTC.
  hour: ResourceStats[];
  height?: number;
}

const MARGIN = { top: 10, right: 12, bottom: 28, left: 44 };

/** D3 1:1 port of the activity-per-hour-of-day bar chart. */
export function HourBarChart({ hour, height = 320 }: HourBarChartProps) {
  const surface = useD3Surface(
    { height, margin: MARGIN },
    ({ g, innerW, innerH, showTip, hideTip }) => {
      const values = Array(24).fill(0) as number[];
      for (const v of hour) values[addTimeOffset(v.name)] = v.totalSeconds;
      const data = values.map((y, h) => ({ h, y }));

      const fg = cssVar("--muted-foreground");
      const border = cssVar("--border");

      const x = d3
        .scaleBand<number>()
        .domain(d3.range(24))
        .range([0, innerW])
        .padding(0.55);
      const yMax = d3.max(data, (d) => d.y) ?? 0;
      const y = d3.scaleLinear().domain([0, yMax || 1]).nice().range([innerH, 0]);

      gridlines(g, y, { span: innerW, stroke: border });

      styleAxis(
        g.append("g").call(d3.axisLeft(y).ticks(5).tickFormat(hoursTickFormat())),
        { fg },
      );

      g.append("text")
        .attr("transform", "rotate(-90)")
        .attr("x", -innerH / 2)
        .attr("y", -MARGIN.left + 12)
        .attr("text-anchor", "middle")
        .attr("fill", fg)
        .style("font-size", "11px")
        .text("Hours");

      styleAxis(
        g
          .append("g")
          .attr("transform", `translate(0,${innerH})`)
          .call(
            d3
              .axisBottom(x)
              .tickValues(d3.range(0, 24, 2))
              .tickFormat((d) => String(d)),
          ),
        { fg, border },
        { domain: "line" },
      );

      g.selectAll("rect.bar")
        .data(data)
        .join("rect")
        .attr("class", "bar")
        .attr("x", (d) => x(d.h) ?? 0)
        .attr("width", x.bandwidth())
        .attr("y", (d) => y(d.y))
        .attr("height", (d) => innerH - y(d.y))
        .attr("rx", 3)
        .attr("fill", colorAt(0))
        .on("mousemove", (event, d) => {
          showTip(event, tooltipHtml(`${d.h}:00`, ["Activity", secondsToHms(d.y)]));
        })
        .on("mouseleave", hideTip);
    },
    [hour],
  );

  if (hour.length === 0) return <EmptyChart height={height} />;

  return <ChartSurface surface={surface} />;
}
