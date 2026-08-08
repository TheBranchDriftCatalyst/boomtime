import { useMemo } from "react";
import * as d3 from "d3";
import { cssVar } from "@/viz/d3/useChartFrame";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";
import { secondsToHms } from "@/lib/utils";
import { tooltipHtml } from "@/viz/d3/tooltip";
import { rankedContent } from "@/viz/d3/tooltipContent";
import { emptyFloor } from "@/viz/d3/color";
import { EmptyChart } from "@/viz/d3/EmptyChart";

interface ContributionCalendarProps {
  // RAW daily series (NOT weekly-bucketed): parallel arrays.
  dates: string[];
  values: number[]; // seconds per day
  // gaka-csx P3 / gaka-nmk: OPTIONAL GitHub contribution-count overlay, aligned
  // index-for-index to `dates`/`values`. When ABSENT the render is
  // byte-identical to the coding-time-only calendar (the additive invariant):
  // no extra DOM, no message. When PRESENT, each day with ≥1 commit gets the
  // commit COUNT drawn as a small, crisp text label centered in the cell —
  // near-white with a dark halo so it reads over both the empty floor and the
  // teal coding-time fill (the old green-on-teal corner triangle camouflaged).
  ghValues?: number[];
}

// gaka-nmk: bumped 13 → 15 so a 1–2 digit commit count fits legibly in-cell.
const CELL = 15;
const GAP = 3;
// gaka-nmk: below this cell size the in-cell count would be cramped/illegible,
// so we fall back to the tooltip only (which still carries the commits row).
const MIN_CELL_FOR_GH_LABEL = 12;
// Near-white label + dark halo for the GH commit count. Chosen over the
// GitHub-green (#39d353) corner mark it replaces: green-on-teal camouflaged,
// whereas a light glyph with a dark stroke reads on every fill in the ramp.
const GH_LABEL_FILL = "#f4fff7";
const GH_LABEL_HALO = "rgba(0, 0, 0, 0.82)";
const WEEKDAY_LABELS = ["", "Mon", "", "Wed", "", "Fri", ""];
const MARGIN = { top: 20, right: 0, bottom: 0, left: 30 };
const GRID_H = 7 * (CELL + GAP); // 7 weekday rows

/**
 * GitHub-style contribution calendar: weeks as columns, weekday rows, a
 * quantized intensity scale (empty floor → primary). Dark-mode native; sizes to
 * content (short ranges are a compact strip, not a stranded cluster in a huge
 * card). The SVG scrolls horizontally when the range is longer than the card.
 */
export function ContributionCalendar({ dates, values, ghValues }: ContributionCalendarProps) {
  // Content height drives the wrapper so short ranges don't strand a tiny grid.
  const svgHeight = MARGIN.top + GRID_H + 4;

  // The overlay is active ONLY when a ghValues array is supplied. Absent ⇒ the
  // draw below never appends a single overlay element, so the DOM is
  // byte-identical to the coding-time-only calendar (gaka-csx invariant A).
  const ghActive = Array.isArray(ghValues);
  const days = useMemo(
    () =>
      dates.map((d, i) => ({
        date: new Date(d),
        value: values[i] ?? 0,
        gh: ghActive ? (ghValues![i] ?? 0) : 0,
      })),
    [dates, values, ghValues, ghActive],
  );
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

      // Intensity via opacity of --primary (avoids interpolating oklch tokens):
      // empty => floor; active days ramp 0.25 → 1.0 across 4 quantized buckets.
      const maxVal = d3.max(days, (d) => d.value) ?? 0;
      const total = d3.sum(days, (d) => d.value);
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
      // gaka-9pt: rank ACTIVE days by seconds desc. Ranking against every day
      // in the window (many 0-seconds days on quiet ranges) would make even
      // top days look unimportant; the calendar is exactly about "which days
      // were my best?".
      const dayRank = new Map<number, number>();
      [...days]
        .filter((d) => d.value > 0)
        .sort((a, b) => b.value - a.value)
        .forEach((d, i) => dayRank.set(d.date.getTime(), i + 1));
      const activeDays = dayRank.size;

      cellG
        .append("rect")
        .attr("width", CELL)
        .attr("height", CELL)
        .attr("rx", 2)
        .attr("fill", base)
        .attr("fill-opacity", (d) => opacity(d.value))
        .on("mousemove", (event, d) => {
          const isPeak = d.value > 0 && d.value === maxVal;
          let rows0: { label: string; value: string; muted?: boolean }[];
          let footer: string | undefined;
          if (d.value > 0) {
            const rk = dayRank.get(d.date.getTime()) ?? 0;
            const built = rankedContent(
              d.value,
              total,
              rk,
              activeDays,
              secondsToHms,
              { shareLabel: "Share of window" },
            );
            rows0 = built.rows;
            // Peak day flag takes precedence over rank in the footer — it's
            // the more prominent signal on the top cell. Rank still surfaces
            // for #2..#N cells via fmtRank.
            footer = isPeak
              ? "Peak day in this window"
              : built.footer || undefined;
          } else {
            rows0 = [{ label: "Activity", value: "No activity" }];
            footer = undefined;
          }
          // gaka-csx P3: surface the day's GitHub commits alongside coding time
          // when the overlay is active. Only appears when there were commits, so
          // the tooltip stays quiet on GH-empty days (and identical when the
          // overlay is absent entirely).
          if (ghActive && d.gh > 0) {
            rows0 = [
              ...rows0,
              {
                label: "GitHub commits",
                value: `${d.gh} commit${d.gh === 1 ? "" : "s"}`,
              },
            ];
          }
          showTip(
            event,
            tooltipHtml({
              title: d3.timeFormat("%a %d %b %Y")(d.date),
              titleSwatch: d.value > 0 ? base : undefined,
              rows: rows0,
              footer,
            }),
          );
        })
        .on("mouseleave", hideTip);

      // gaka-nmk: GitHub commit overlay, now the COUNT as a text label instead
      // of the old green corner triangle (which was invisible green-on-teal).
      // One label per day that had commits, centered in the cell, drawn over
      // the coding-time fill. Rendered ONLY when the overlay is active, there is
      // at least one commit in the window, AND the cell is large enough for the
      // number to read — so an absent (or all-zero) overlay adds no DOM,
      // preserving invariant (A). Below the size threshold we skip the label and
      // let the tooltip's "GitHub commits" row carry it.
      const ghMax = ghActive ? (d3.max(days, (d) => d.gh) ?? 0) : 0;
      if (ghActive && ghMax > 0 && CELL >= MIN_CELL_FOR_GH_LABEL) {
        cellG
          .filter((d) => d.gh > 0)
          .append("text")
          .attr("class", "gh-count")
          .attr("x", CELL / 2)
          .attr("y", CELL / 2)
          .attr("text-anchor", "middle")
          .attr("dominant-baseline", "central")
          .style("font-size", "9px")
          .style("font-weight", "700")
          .style("font-family", "var(--font-mono, ui-monospace, monospace)")
          // paint-order:stroke draws the dark halo UNDER the fill, so the light
          // glyph stays crisp over both the empty floor and the neon teal fill.
          .style("paint-order", "stroke")
          .style("stroke", GH_LABEL_HALO)
          .style("stroke-width", "2.5px")
          .style("stroke-linejoin", "round")
          .attr("fill", GH_LABEL_FILL)
          .style("pointer-events", "none")
          .text((d) => d.gh);
      }

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
    [days, ghActive],
  );

  if (days.length === 0) return <EmptyChart height={svgHeight} />;

  // gaka-k2p: always left-align. Previously a short window was centered,
  // which stranded a big empty white gap to the LEFT of the cells on the
  // public dashboard's full-bleed h=3 calendar card. Left-aligning keeps
  // the axis labels flush and lets long windows scroll horizontally in
  // the same wrapper.
  return (
    <ChartSurface
      surface={surface}
      style={{
        overflowX: "auto",
        display: "flex",
        justifyContent: "flex-start",
      }}
    />
  );
}
