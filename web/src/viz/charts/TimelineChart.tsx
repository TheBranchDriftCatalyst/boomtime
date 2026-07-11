import { useMemo } from "react";
import * as d3 from "d3";
import { secondsToHms, truncate } from "@/lib/utils";
import { cssVar } from "@/viz/d3/useChartFrame";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";
import { tooltipHtml } from "@/viz/d3/tooltip";
import { styleAxis } from "@/viz/d3/axes";
import { colorAt } from "@/viz/d3/color";
import { renderLegend } from "@/viz/d3/legend";
import { EmptyChart } from "@/viz/d3/EmptyChart";
import type { TimelinePayload } from "@/types/api";

export interface TimelineChartProps {
  timeline: TimelinePayload | undefined;
  height?: number;
}

interface Segment {
  lang: string;
  start: Date;
  end: Date;
  colorIndex: number;
}

const LEGEND_H = 24;
const MARGIN = { top: LEGEND_H + 6, right: 16, bottom: 26, left: 80 };

/** D3 1:1 port of the recent-timeline rangeBar (lanes by language). */
export function TimelineChart({ timeline, height = 350 }: TimelineChartProps) {
  const langs = useMemo(() => timeline?.langs ?? {}, [timeline]);
  const langNames = useMemo(() => Object.keys(langs), [langs]);

  const segments = useMemo(() => {
    const segs: Segment[] = [];
    langNames.forEach((lang, i) => {
      for (const v of langs[lang]) {
        segs.push({
          lang,
          start: new Date(v.rangeStart),
          end: new Date(v.rangeEnd),
          colorIndex: i,
        });
      }
    });
    return segs;
  }, [langs, langNames]);

  const surface = useD3Surface(
    { height, margin: MARGIN },
    ({ svg, g, innerW, innerH, showTip, hideTip }) => {
      if (segments.length === 0) return;

      const fg = cssVar("--muted-foreground");
      const border = cssVar("--border");

      const tMin = d3.min(segments, (s) => s.start) as Date;
      const tMax = d3.max(segments, (s) => s.end) as Date;
      const x = d3.scaleTime().domain([tMin, tMax]).range([0, innerW]);
      const y = d3
        .scaleBand<string>()
        .domain(langNames)
        .range([0, innerH])
        .padding(0.3);

      // Vertical gridlines + time axis.
      const xAxis = styleAxis(
        g
          .append("g")
          .attr("transform", `translate(0,${innerH})`)
          .call(
            d3.axisBottom(x).ticks(6).tickFormat((d) => d3.timeFormat("%H:%M")(d as Date)),
          ),
        { fg, border },
        { domain: "line" },
      );
      xAxis
        .selectAll(".tick")
        .append("line")
        .attr("y1", 0)
        .attr("y2", -innerH)
        .attr("stroke", border)
        .attr("stroke-dasharray", "3");

      // Lane labels (truncated to 10; full on hover).
      styleAxis(
        g
          .append("g")
          .call(d3.axisLeft(y).tickSize(0).tickFormat((d) => truncate(String(d), 10))),
        { fg },
      )
        .selectAll<SVGTextElement, string>("text")
        .append("title")
        .text((d) => String(d));

      g.selectAll("rect.seg")
        .data(segments)
        .join("rect")
        .attr("class", "seg")
        .attr("x", (d) => x(d.start))
        .attr("y", (d) => y(d.lang) ?? 0)
        .attr("width", (d) => Math.max(1, x(d.end) - x(d.start)))
        .attr("height", y.bandwidth())
        .attr("rx", 2)
        .attr("fill", (d) => colorAt(d.colorIndex))
        .on("mousemove", (event, d) => {
          const dur = (d.end.getTime() - d.start.getTime()) / 1000;
          showTip(
            event,
            tooltipHtml(
              d.lang,
              `${d3.timeFormat("%d %b, %H:%M")(d.start)} → ${d3.timeFormat("%H:%M")(d.end)}`,
              secondsToHms(dur),
            ),
          );
        })
        .on("mouseleave", hideTip);

      // Legend (top-left), overflow collapsed to "+N more".
      renderLegend(
        svg,
        langNames.map((lang, i) => ({ label: lang, color: colorAt(i) })),
        { x: MARGIN.left, y: 4, fg, maxWidth: innerW, gap: 30 },
      );
    },
    [segments, langNames],
  );

  if (segments.length === 0) return <EmptyChart height={height} />;

  return <ChartSurface surface={surface} />;
}
