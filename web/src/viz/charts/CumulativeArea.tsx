import { useMemo } from "react";
import * as d3 from "d3";
import { secondsToHms } from "@/lib/utils";
import { cssVar } from "@/viz/d3/useChartFrame";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";
import { tooltipHtml } from "@/viz/d3/tooltip";
import {
  formatDay,
  gridlines,
  hoursTickFormat,
  styleAxis,
  thinnedDateTicks,
} from "@/viz/d3/axes";
import { colorAt } from "@/viz/d3/color";
import { EmptyChart } from "@/viz/d3/EmptyChart";

interface CumulativeAreaProps {
  // Bucketed parallel arrays (use the page's ~weekly buckets).
  dates: string[];
  values: number[]; // seconds per bucket
  height?: number;
}

const MARGIN = { top: 10, right: 16, bottom: 28, left: 52 };

/** Running-total (cumulative) coding time as a filled area + line. */
export function CumulativeArea({
  dates,
  values,
  height = 300,
}: CumulativeAreaProps) {
  const data = useMemo(() => {
    let acc = 0;
    return dates.map((d, i) => {
      acc += values[i] ?? 0;
      return { date: new Date(d), cum: acc };
    });
  }, [dates, values]);

  const surface = useD3Surface(
    { height, margin: MARGIN },
    ({ g, innerW, innerH, showTip, hideTip }) => {
      if (data.length === 0) return;

      const fg = cssVar("--muted-foreground");
      const border = cssVar("--border");
      const color = colorAt(3);

      const x = d3
        .scaleTime()
        .domain(d3.extent(data, (d) => d.date) as [Date, Date])
        .range([0, innerW]);
      const yMax = d3.max(data, (d) => d.cum) ?? 0;
      const y = d3.scaleLinear().domain([0, yMax || 1]).nice().range([innerH, 0]);

      // Gridlines + hours y-axis.
      gridlines(g, y, { span: innerW, stroke: border });
      styleAxis(
        g
          .append("g")
          .call(
            d3
              .axisLeft(y)
              .ticks(5)
              .tickFormat(hoursTickFormat({ decimals: 0, suffix: "h" })),
          ),
        { fg },
      );

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

      const area = d3
        .area<{ date: Date; cum: number }>()
        .x((d) => x(d.date))
        .y0(innerH)
        .y1((d) => y(d.cum))
        .curve(d3.curveMonotoneX);
      const line = d3
        .line<{ date: Date; cum: number }>()
        .x((d) => x(d.date))
        .y((d) => y(d.cum))
        .curve(d3.curveMonotoneX);

      g.append("path").datum(data).attr("d", area).attr("fill", color).attr("fill-opacity", 0.18);
      g.append("path")
        .datum(data)
        .attr("d", line)
        .attr("fill", "none")
        .attr("stroke", color)
        .attr("stroke-width", 2);

      // Hover dots.
      g.selectAll("circle.pt")
        .data(data)
        .join("circle")
        .attr("class", "pt")
        .attr("cx", (d) => x(d.date))
        .attr("cy", (d) => y(d.cum))
        .attr("r", 8)
        .attr("fill", "transparent")
        .on("mousemove", (event, d) => {
          showTip(
            event,
            tooltipHtml(d3.timeFormat("%d %b %Y")(d.date), [
              "Total",
              secondsToHms(d.cum),
            ]),
          );
        })
        .on("mouseleave", hideTip);
    },
    [data],
  );

  if (data.length === 0) return <EmptyChart height={height} />;

  return <ChartSurface surface={surface} />;
}
