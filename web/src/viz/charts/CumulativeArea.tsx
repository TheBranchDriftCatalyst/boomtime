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

interface CumulativeAreaProps {
  // Bucketed parallel arrays (use the page's ~weekly buckets).
  dates: string[];
  values: number[]; // seconds per bucket
  height?: number;
}

/** Running-total (cumulative) coding time as a filled area + line. */
export function CumulativeArea({
  dates,
  values,
  height = 300,
}: CumulativeAreaProps) {
  const { ref, frame } = useChartFrame(height);
  const svgRef = useRef<SVGSVGElement | null>(null);

  const data = useMemo(() => {
    let acc = 0;
    return dates.map((d, i) => {
      acc += values[i] ?? 0;
      return { date: new Date(d), cum: acc };
    });
  }, [dates, values]);

  useEffect(() => {
    const svg = d3.select(svgRef.current);
    svg.selectAll("*").remove();
    const container = ref.current;
    if (!container || frame.width === 0 || data.length === 0) return;

    const fg = cssVar("--muted-foreground");
    const border = cssVar("--border");
    const color = CHART_COLORS[3];

    const width = frame.width;
    const margin = { top: 10, right: 16, bottom: 28, left: 52 };
    const innerW = width - margin.left - margin.right;
    const innerH = height - margin.top - margin.bottom;

    const g = svg
      .attr("width", width)
      .attr("height", height)
      .append("g")
      .attr("transform", `translate(${margin.left},${margin.top})`);

    const x = d3
      .scaleTime()
      .domain(d3.extent(data, (d) => d.date) as [Date, Date])
      .range([0, innerW]);
    const yMax = d3.max(data, (d) => d.cum) ?? 0;
    const y = d3.scaleLinear().domain([0, yMax || 1]).nice().range([innerH, 0]);

    // Gridlines + hours y-axis.
    g.append("g")
      .call(d3.axisLeft(y).ticks(5).tickSize(-innerW).tickFormat(() => ""))
      .call((sel) => sel.select(".domain").remove())
      .selectAll("line")
      .attr("stroke", border)
      .attr("stroke-dasharray", "4");
    g.append("g")
      .call(
        d3.axisLeft(y).ticks(5).tickFormat((v) => `${(Number(v) / 3600).toFixed(0)}h`),
      )
      .call((sel) => sel.select(".domain").remove())
      .selectAll("text")
      .attr("fill", fg)
      .style("font-size", "11px");

    const tickEvery = Math.ceil(data.length / 8) || 1;
    g.append("g")
      .attr("transform", `translate(0,${innerH})`)
      .call(
        d3
          .axisBottom(x)
          .tickValues(data.map((d) => d.date).filter((_, i) => i % tickEvery === 0))
          .tickFormat((d) => d3.timeFormat("%d %b")(d as Date)),
      )
      .call((sel) => sel.select(".domain").attr("stroke", border))
      .selectAll("text")
      .attr("fill", fg)
      .style("font-size", "11px");

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
    const tip: TooltipSelection = createTooltip(container);
    g.selectAll("circle.pt")
      .data(data)
      .join("circle")
      .attr("class", "pt")
      .attr("cx", (d) => x(d.date))
      .attr("cy", (d) => y(d.cum))
      .attr("r", 8)
      .attr("fill", "transparent")
      .on("mousemove", (event, d) => {
        showTooltip(
          tip,
          container,
          event,
          `<div style="font-weight:600">${d3.timeFormat("%d %b %Y")(d.date)}</div>` +
            `Total: ${secondsToHms(d.cum)}`,
        );
      })
      .on("mouseleave", () => hideTooltip(tip));

    return () => {
      tip.remove();
    };
  }, [data, height, frame.width, frame.themeKey, ref]);

  if (data.length === 0) return <EmptyChart height={height} />;

  return (
    <div ref={ref} style={{ position: "relative", width: "100%", height }}>
      <svg ref={svgRef} />
    </div>
  );
}
