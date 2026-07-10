import { useEffect, useMemo, useRef, useState } from "react";
import * as d3 from "d3";
import { cssVar, useChartFrame } from "@/viz/d3/useChartFrame";
import { secondsToHms } from "@/lib/utils";
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
}

const CELL = 13;
const GAP = 3;
const WEEKDAY_LABELS = ["", "Mon", "", "Wed", "", "Fri", ""];
const MARGIN_TOP = 20;
const MARGIN_LEFT = 30;
const GRID_H = 7 * (CELL + GAP); // 7 weekday rows

/**
 * GitHub-style contribution calendar: weeks as columns, weekday rows, a
 * quantized intensity scale (empty floor → primary). Dark-mode native; sizes to
 * content (short ranges are a compact strip, not a stranded cluster in a huge
 * card). The SVG scrolls horizontally when the range is longer than the card.
 */
export function ContributionCalendar({ dates, values }: ContributionCalendarProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const svgRef = useRef<SVGSVGElement | null>(null);
  // Content height drives the wrapper so short ranges don't strand a tiny grid.
  const svgHeight = MARGIN_TOP + GRID_H + 4;
  const { frame } = useChartFrame(svgHeight);

  const days = useMemo(
    () => dates.map((d, i) => ({ date: new Date(d), value: values[i] ?? 0 })),
    [dates, values],
  );
  const [svgWidth, setSvgWidth] = useState(0);

  useEffect(() => {
    const svg = d3.select(svgRef.current);
    svg.selectAll("*").remove();
    const container = containerRef.current;
    if (!container || days.length === 0) return;

    const fg = cssVar("--muted-foreground");
    const base = cssVar("--primary");
    const dark = document.documentElement.classList.contains("dark");
    // Empty-day floor: a visible tone distinct from the near-black card so empty
    // cells read as "empty" but the grid stays visible. Fixed hex (not the oklch
    // --muted token, which d3 can't parse).
    const emptyCell = dark ? "#232a36" : "#eceef2";

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
    const svgW = MARGIN_LEFT + gridW + 4;
    setSvgWidth(svgW);

    // Intensity via opacity of --primary (avoids interpolating oklch tokens):
    // empty => floor; active days ramp 0.25 → 1.0 across 4 quantized buckets.
    const maxVal = d3.max(days, (d) => d.value) ?? 0;
    const opacity = (v: number): number => {
      if (v <= 0) return 0;
      const t = maxVal > 0 ? v / maxVal : 0;
      const bucket = Math.min(4, Math.floor(t * 4) + 1); // 1..4
      return 0.25 + (bucket / 4) * 0.75;
    };

    svg.attr("width", svgW).attr("height", svgHeight);
    const g = svg.append("g").attr("transform", `translate(${MARGIN_LEFT},${MARGIN_TOP})`);

    const tip: TooltipSelection = createTooltip(container);

    const cellG = g
      .selectAll("g.day")
      .data(days)
      .join("g")
      .attr("class", "day")
      .attr(
        "transform",
        (d) => `translate(${col(d.date) * (CELL + GAP)},${row(d.date) * (CELL + GAP)})`,
      );
    // Floor rect (always visible) + primary rect with per-cell opacity.
    cellG
      .append("rect")
      .attr("width", CELL)
      .attr("height", CELL)
      .attr("rx", 2)
      .attr("fill", emptyCell);
    cellG
      .append("rect")
      .attr("width", CELL)
      .attr("height", CELL)
      .attr("rx", 2)
      .attr("fill", base)
      .attr("fill-opacity", (d) => opacity(d.value))
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

    // Month labels: only when the month changes AND there's min spacing from
    // the previously placed label (avoids "JunJul" overlap on short ranges).
    const MIN_LABEL_GAP = 24; // px
    const monthTicks: { x: number; label: string }[] = [];
    let lastMonth = -1;
    let lastX = -Infinity;
    for (const d of days) {
      const m = d.date.getMonth();
      if (m === lastMonth) continue;
      lastMonth = m;
      const x = col(d.date) * (CELL + GAP);
      if (x - lastX < MIN_LABEL_GAP) continue;
      monthTicks.push({ x, label: d3.timeFormat("%b")(d.date) });
      lastX = x;
    }
    svg
      .append("g")
      .attr("transform", `translate(${MARGIN_LEFT},12)`)
      .selectAll("text.month")
      .data(monthTicks)
      .join("text")
      .attr("class", "month")
      .attr("x", (d) => d.x)
      .attr("fill", fg)
      .style("font-size", "10px")
      .text((d) => d.label);

    return () => {
      tip.remove();
    };
  }, [days, frame.themeKey, svgHeight]);

  if (days.length === 0) return <EmptyChart height={svgHeight} />;

  // Center a short calendar; left-align (and scroll) a long one.
  const fits = svgWidth > 0 && svgWidth <= frame.width;
  return (
    <div
      ref={containerRef}
      style={{
        position: "relative",
        width: "100%",
        height: svgHeight,
        overflowX: "auto",
        display: "flex",
        justifyContent: fits ? "center" : "flex-start",
      }}
    >
      <svg ref={svgRef} />
    </div>
  );
}
