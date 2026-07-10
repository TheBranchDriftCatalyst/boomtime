import { useEffect, useMemo, useRef } from "react";
import * as d3 from "d3";
import { CHART_COLORS } from "@/lib/config";
import { secondsToHms, truncate } from "@/lib/utils";
import { cssVar, useChartFrame } from "@/viz/d3/useChartFrame";
import {
  createTooltip,
  hideTooltip,
  showTooltip,
  type TooltipSelection,
} from "@/viz/d3/tooltip";
import type { TimelineChartProps } from "@/components/charts/types";

interface Segment {
  lang: string;
  start: Date;
  end: Date;
  colorIndex: number;
}

/** D3 1:1 port of the recent-timeline rangeBar (lanes by language). */
export function TimelineChartD3({ timeline, height = 350 }: TimelineChartProps) {
  const { ref, frame } = useChartFrame(height);
  const svgRef = useRef<SVGSVGElement | null>(null);

  const langs = useMemo(() => timeline?.langs ?? {}, [timeline]);
  const langNames = useMemo(() => Object.keys(langs), [langs]);

  useEffect(() => {
    const svg = d3.select(svgRef.current);
    svg.selectAll("*").remove();
    const container = ref.current;
    if (!container || frame.width === 0 || langNames.length === 0) return;

    const segments: Segment[] = [];
    langNames.forEach((lang, i) => {
      for (const v of langs[lang]) {
        segments.push({
          lang,
          start: new Date(v.rangeStart),
          end: new Date(v.rangeEnd),
          colorIndex: i,
        });
      }
    });
    if (segments.length === 0) return;

    const fg = cssVar("--muted-foreground");
    const border = cssVar("--border");
    const width = frame.width;
    const legendH = 24;
    const margin = { top: legendH + 6, right: 16, bottom: 26, left: 80 };
    const innerW = width - margin.left - margin.right;
    const innerH = height - margin.top - margin.bottom;

    const svgRoot = svg.attr("width", width).attr("height", height);
    const g = svgRoot
      .append("g")
      .attr("transform", `translate(${margin.left},${margin.top})`);

    const tMin = d3.min(segments, (s) => s.start) as Date;
    const tMax = d3.max(segments, (s) => s.end) as Date;
    const x = d3.scaleTime().domain([tMin, tMax]).range([0, innerW]);
    const y = d3
      .scaleBand<string>()
      .domain(langNames)
      .range([0, innerH])
      .padding(0.3);

    // Vertical gridlines + time axis.
    const xAxis = g
      .append("g")
      .attr("transform", `translate(0,${innerH})`)
      .call(d3.axisBottom(x).ticks(6).tickFormat((d) => d3.timeFormat("%H:%M")(d as Date)));
    xAxis.select(".domain").attr("stroke", border);
    xAxis.selectAll("text").attr("fill", fg).style("font-size", "11px");
    xAxis
      .selectAll(".tick")
      .append("line")
      .attr("y1", 0)
      .attr("y2", -innerH)
      .attr("stroke", border)
      .attr("stroke-dasharray", "3");

    // Lane labels (truncated to 10 like the Apex yaxis formatter).
    g.append("g")
      .call(d3.axisLeft(y).tickSize(0).tickFormat((d) => truncate(String(d), 10)))
      .call((sel) => sel.select(".domain").remove())
      .selectAll("text")
      .attr("fill", fg)
      .style("font-size", "11px");

    const tip: TooltipSelection = createTooltip(container);

    g.selectAll("rect.seg")
      .data(segments)
      .join("rect")
      .attr("class", "seg")
      .attr("x", (d) => x(d.start))
      .attr("y", (d) => y(d.lang) ?? 0)
      .attr("width", (d) => Math.max(1, x(d.end) - x(d.start)))
      .attr("height", y.bandwidth())
      .attr("rx", 2)
      .attr("fill", (d) => CHART_COLORS[d.colorIndex % CHART_COLORS.length])
      .on("mousemove", (event, d) => {
        const dur = (d.end.getTime() - d.start.getTime()) / 1000;
        showTooltip(
          tip,
          container,
          event,
          `<div style="font-weight:600">${d.lang}</div>` +
            `${d3.timeFormat("%d %b, %H:%M")(d.start)} → ${d3.timeFormat("%H:%M")(
              d.end,
            )}<br/>${secondsToHms(dur)}`,
        );
      })
      .on("mouseleave", () => hideTooltip(tip));

    // Legend (top-left), matching Apex legend position.
    const legend = svgRoot
      .append("g")
      .attr("transform", `translate(${margin.left},4)`);
    let offset = 0;
    langNames.forEach((lang, i) => {
      const item = legend.append("g").attr("transform", `translate(${offset},0)`);
      item
        .append("rect")
        .attr("width", 10)
        .attr("height", 10)
        .attr("rx", 2)
        .attr("y", 3)
        .attr("fill", CHART_COLORS[i % CHART_COLORS.length]);
      const label = item
        .append("text")
        .attr("x", 14)
        .attr("y", 12)
        .attr("fill", fg)
        .style("font-size", "11px")
        .text(lang);
      const w = (label.node()?.getComputedTextLength() ?? 40) + 30;
      offset += w;
    });

    return () => {
      tip.remove();
    };
  }, [ref, langs, langNames, height, frame.width, frame.themeKey]);

  return (
    <div ref={ref} style={{ position: "relative", width: "100%", height }}>
      <svg ref={svgRef} />
    </div>
  );
}
