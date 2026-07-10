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

interface AuthoringVsReadingProps {
  writeSeconds: number | undefined;
  readSeconds: number | undefined;
  // Bucketed write-ratio series (0..1) aligned to `dates`.
  dates: string[];
  ratio: number[];
  height?: number;
}

const WRITE_COLOR = CHART_COLORS[2]; // green-ish
const READ_COLOR = CHART_COLORS[0]; // blue

/** Donut of authoring vs reading time + a small write-ratio-over-time line. */
export function AuthoringVsReading({
  writeSeconds,
  readSeconds,
  dates,
  ratio,
  height = 300,
}: AuthoringVsReadingProps) {
  const { ref, frame } = useChartFrame(height);
  const svgRef = useRef<SVGSVGElement | null>(null);

  const write = writeSeconds ?? 0;
  const read = readSeconds ?? 0;
  const hasData =
    (writeSeconds !== undefined || readSeconds !== undefined) &&
    write + read > 0;

  const slices = useMemo(
    () => [
      { label: "Authoring", value: write, color: WRITE_COLOR },
      { label: "Reading", value: read, color: READ_COLOR },
    ],
    [write, read],
  );

  useEffect(() => {
    const svg = d3.select(svgRef.current);
    svg.selectAll("*").remove();
    const container = ref.current;
    if (!container || frame.width === 0 || !hasData) return;

    const fg = cssVar("--foreground");
    const card = cssVar("--card");
    const border = cssVar("--border");
    const muted = cssVar("--muted-foreground");
    const width = frame.width;

    // Donut occupies the left ~45%; the ratio line the right ~55%.
    const donutW = Math.min(width * 0.45, 220);
    const radius = Math.min(donutW, height) / 2 - 8;
    const cx = donutW / 2;
    const cy = height / 2;

    const g = svg.attr("width", width).attr("height", height).append("g");
    const donut = g.append("g").attr("transform", `translate(${cx},${cy})`);

    const total = write + read;
    const pie = d3.pie<{ label: string; value: number; color: string }>().sort(null).value((d) => d.value);
    const arc = d3
      .arc<d3.PieArcDatum<{ label: string; value: number; color: string }>>()
      .innerRadius(radius * 0.58)
      .outerRadius(radius);

    const tip: TooltipSelection = createTooltip(container);

    donut
      .selectAll("path")
      .data(pie(slices))
      .join("path")
      .attr("d", arc)
      .attr("fill", (d) => d.data.color)
      .attr("stroke", card)
      .attr("stroke-width", 1)
      .on("mousemove", (event, d) => {
        showTooltip(
          tip,
          container,
          event,
          `<div style="font-weight:600">${d.data.label}</div>${secondsToHms(
            d.data.value,
          )} (${Math.round((d.data.value / total) * 100)}%)`,
        );
      })
      .on("mouseleave", () => hideTooltip(tip));

    // Center label = authoring share.
    donut
      .append("text")
      .attr("text-anchor", "middle")
      .attr("dy", "-0.1em")
      .attr("fill", fg)
      .style("font-size", "20px")
      .style("font-weight", "700")
      .text(`${Math.round((write / total) * 100)}%`);
    donut
      .append("text")
      .attr("text-anchor", "middle")
      .attr("dy", "1.3em")
      .attr("fill", muted)
      .style("font-size", "10px")
      .text("authoring");

    // Right: write-ratio-over-time line (0..1).
    const lineData = dates.map((d, i) => ({ date: new Date(d), r: ratio[i] ?? 0 }));
    if (lineData.length >= 2) {
      const lm = { top: 12, right: 12, bottom: 22, left: 30 };
      const lx0 = donutW + 8;
      const innerW = width - lx0 - lm.right;
      const innerH = height - lm.top - lm.bottom;
      const lg = g.append("g").attr("transform", `translate(${lx0},${lm.top})`);

      const x = d3
        .scaleTime()
        .domain(d3.extent(lineData, (d) => d.date) as [Date, Date])
        .range([0, innerW]);
      const y = d3.scaleLinear().domain([0, 1]).range([innerH, 0]);

      lg.append("g")
        .call(d3.axisLeft(y).ticks(3).tickFormat((v) => `${Math.round(Number(v) * 100)}%`))
        .call((sel) => sel.select(".domain").remove())
        .selectAll("text")
        .attr("fill", muted)
        .style("font-size", "10px");
      lg.append("g")
        .attr("transform", `translate(0,${innerH})`)
        .call(d3.axisBottom(x).ticks(4).tickFormat((d) => d3.timeFormat("%d %b")(d as Date)))
        .call((sel) => sel.select(".domain").attr("stroke", border))
        .selectAll("text")
        .attr("fill", muted)
        .style("font-size", "10px");

      const line = d3
        .line<{ date: Date; r: number }>()
        .x((d) => x(d.date))
        .y((d) => y(d.r))
        .curve(d3.curveMonotoneX);
      lg.append("path")
        .datum(lineData)
        .attr("d", line)
        .attr("fill", "none")
        .attr("stroke", WRITE_COLOR)
        .attr("stroke-width", 2);
      lg.append("text")
        .attr("x", 0)
        .attr("y", -2)
        .attr("fill", muted)
        .style("font-size", "10px")
        .text("Authoring ratio over time");
    }

    return () => {
      tip.remove();
    };
  }, [hasData, slices, write, read, dates, ratio, height, frame.width, frame.themeKey, ref]);

  if (!hasData) return <EmptyChart height={height} />;

  return (
    <div ref={ref} style={{ position: "relative", width: "100%", height }}>
      <svg ref={svgRef} />
    </div>
  );
}
