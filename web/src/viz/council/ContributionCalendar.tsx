import { useEffect, useMemo, useRef } from "react";
import * as d3 from "d3";
import { secondsToHms } from "@/lib/utils";
import { cssVar, useChartFrame } from "@/viz/d3/useChartFrame";
import {
  createTooltip,
  hideTooltip,
  showTooltip,
  type TooltipSelection,
} from "@/viz/d3/tooltip";
import { EmptyChart } from "@/viz/d3/EmptyChart";

interface ContributionCalendarProps {
  // RAW daily series (NOT weekly-bucketed): parallel arrays.
  dates: string[];
  values: number[]; // seconds per day
  height?: number;
}

const CELL = 13;
const GAP = 3;
const WEEKDAY_LABELS = ["", "Mon", "", "Wed", "", "Fri", ""];

/**
 * GitHub-style contribution calendar: weeks as columns, weekday rows, a
 * quantized sequential color scale. Flagship D3-only viz. Dark-mode native via
 * theme tokens; the SVG width is fixed by the number of weeks and scrolls if
 * the container is narrow.
 */
export function ContributionCalendar({
  dates,
  values,
  height = 180,
}: ContributionCalendarProps) {
  const { frame } = useChartFrame(height);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const svgRef = useRef<SVGSVGElement | null>(null);

  const days = useMemo(
    () =>
      dates.map((d, i) => ({ date: new Date(d), value: values[i] ?? 0 })),
    [dates, values],
  );

  useEffect(() => {
    const svg = d3.select(svgRef.current);
    svg.selectAll("*").remove();
    const container = containerRef.current;
    if (!container || days.length === 0) return;

    const fg = cssVar("--muted-foreground");
    const emptyCell = cssVar("--muted");
    const base = cssVar("--primary");

    const marginTop = 20;
    const marginLeft = 30;

    // Column index per day = whole weeks since the first day's week start
    // (Sunday = day 0). Row = weekday.
    const first = days[0].date;
    const firstWeekStart = d3.timeWeek.floor(first);
    const col = (d: Date) =>
      Math.round(
        (d3.timeWeek.floor(d).getTime() - firstWeekStart.getTime()) /
          (7 * 86_400_000),
      );
    const row = (d: Date) => d.getDay();

    const numWeeks = col(days[days.length - 1].date) + 1;
    const gridW = numWeeks * (CELL + GAP);
    const svgW = marginLeft + gridW + 4;
    const svgH = marginTop + 7 * (CELL + GAP);

    // Quantized sequential scale: 5 buckets from empty → primary.
    const maxVal = d3.max(days, (d) => d.value) ?? 0;
    const color = (v: number): string => {
      if (v <= 0) return emptyCell;
      const t = maxVal > 0 ? v / maxVal : 0;
      const bucket = Math.min(4, Math.floor(t * 4) + 1) / 4; // 0.25..1
      return d3.interpolateRgb(emptyCell, base)(0.15 + bucket * 0.85);
    };

    svg.attr("width", svgW).attr("height", svgH);
    const g = svg.append("g").attr("transform", `translate(${marginLeft},${marginTop})`);

    const tip: TooltipSelection = createTooltip(container);

    g.selectAll("rect.day")
      .data(days)
      .join("rect")
      .attr("class", "day")
      .attr("x", (d) => col(d.date) * (CELL + GAP))
      .attr("y", (d) => row(d.date) * (CELL + GAP))
      .attr("width", CELL)
      .attr("height", CELL)
      .attr("rx", 2)
      .attr("fill", (d) => color(d.value))
      .on("mousemove", (event, d) => {
        showTooltip(
          tip,
          container,
          event,
          `<div style="font-weight:600">${d3.timeFormat("%a %d %b %Y")(d.date)}</div>` +
            `${d.value > 0 ? secondsToHms(d.value) : "No activity"}`,
        );
      })
      .on("mouseleave", () => hideTooltip(tip));

    // Weekday row labels.
    g.selectAll("text.wd")
      .data(WEEKDAY_LABELS)
      .join("text")
      .attr("class", "wd")
      .attr("x", -6)
      .attr("y", (_d, i) => i * (CELL + GAP) + CELL - 2)
      .attr("text-anchor", "end")
      .attr("fill", fg)
      .style("font-size", "9px")
      .text((d) => d);

    // Month labels along the top (at the first week that starts a new month).
    const monthTicks: { col: number; label: string }[] = [];
    let lastMonth = -1;
    for (const d of days) {
      const m = d.date.getMonth();
      if (m !== lastMonth) {
        monthTicks.push({ col: col(d.date), label: d3.timeFormat("%b")(d.date) });
        lastMonth = m;
      }
    }
    svg
      .append("g")
      .attr("transform", `translate(${marginLeft},12)`)
      .selectAll("text.month")
      .data(monthTicks)
      .join("text")
      .attr("class", "month")
      .attr("x", (d) => d.col * (CELL + GAP))
      .attr("fill", fg)
      .style("font-size", "10px")
      .text((d) => d.label);

    return () => {
      tip.remove();
    };
  }, [days, frame.themeKey, height]);

  if (days.length === 0) return <EmptyChart height={height} />;

  return (
    <div
      ref={containerRef}
      style={{ position: "relative", width: "100%", height, overflowX: "auto" }}
    >
      <svg ref={svgRef} />
    </div>
  );
}
