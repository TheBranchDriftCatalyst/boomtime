import { useMemo, useState } from "react";
import * as d3 from "d3";
import { cssVar } from "@/viz/d3/useChartFrame";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";
import { secondsToHms } from "@/lib/utils";
import { tooltipHtml } from "@/viz/d3/tooltip";
import { emptyFloor } from "@/viz/d3/color";
import { EmptyChart } from "@/viz/d3/EmptyChart";

interface ContributionCalendarProps {
  // RAW daily series (NOT weekly-bucketed): parallel arrays.
  dates: string[];
  values: number[]; // seconds per day
}

const CELL = 13;
const GAP = 3;
const WEEKDAY_LABELS = ["", "Mon", "", "Wed", "", "Fri", ""];
const MARGIN = { top: 20, right: 0, bottom: 0, left: 30 };
const GRID_H = 7 * (CELL + GAP); // 7 weekday rows

/**
 * GitHub-style contribution calendar: weeks as columns, weekday rows, a
 * quantized intensity scale (empty floor → primary). Dark-mode native; sizes to
 * content (short ranges are a compact strip, not a stranded cluster in a huge
 * card). The SVG scrolls horizontally when the range is longer than the card.
 */
export function ContributionCalendar({ dates, values }: ContributionCalendarProps) {
  // Content height drives the wrapper so short ranges don't strand a tiny grid.
  const svgHeight = MARGIN.top + GRID_H + 4;

  const days = useMemo(
    () => dates.map((d, i) => ({ date: new Date(d), value: values[i] ?? 0 })),
    [dates, values],
  );
  const [svgWidth, setSvgWidth] = useState(0);

  // Content-sized: the draw owns the svg width and doesn't re-run on frame
  // width changes (the centering below is pure JSX off the measured frame).
  const surface = useD3Surface(
    { height: svgHeight, margin: MARGIN, sizeToFrame: false },
    ({ svg, g, showTip, hideTip }) => {
      if (days.length === 0) return;

      const fg = cssVar("--muted-foreground");
      const base = cssVar("--primary");
      // Empty-day floor: a visible tone distinct from the near-black card so
      // empty cells read as "empty" but the grid stays visible.
      const emptyCell = emptyFloor();

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
      const svgW = MARGIN.left + gridW + 4;
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

      svg.attr("width", svgW);

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
          showTip(
            event,
            tooltipHtml(
              d3.timeFormat("%a %d %b %Y")(d.date),
              d.value > 0 ? secondsToHms(d.value) : "No activity",
            ),
          );
        })
        .on("mouseleave", hideTip);

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
        .attr("transform", `translate(${MARGIN.left},12)`)
        .selectAll("text.month")
        .data(monthTicks)
        .join("text")
        .attr("class", "month")
        .attr("x", (d) => d.x)
        .attr("fill", fg)
        .style("font-size", "10px")
        .text((d) => d.label);
    },
    [days],
  );

  if (days.length === 0) return <EmptyChart height={svgHeight} />;

  // Center a short calendar; left-align (and scroll) a long one.
  const fits = svgWidth > 0 && svgWidth <= surface.frame.width;
  return (
    <ChartSurface
      surface={surface}
      style={{
        overflowX: "auto",
        display: "flex",
        justifyContent: fits ? "center" : "flex-start",
      }}
    />
  );
}
