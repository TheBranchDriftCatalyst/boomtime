import * as d3 from "d3";
import { secondsToHms } from "@shared/lib/utils";
import { cssVar } from "@shared/viz/d3/useChartFrame";
import { useD3Surface } from "@shared/viz/d3/useD3Surface";
import { ChartSurface } from "@shared/viz/d3/ChartSurface";
import { tooltipHtml } from "@shared/viz/d3/tooltip";
import { rankedContent } from "@shared/viz/d3/tooltipContent";
import { styleAxis } from "@shared/viz/d3/axes";
import { colorAt } from "@shared/viz/d3/color";
import { EmptyChart } from "@shared/viz/d3/EmptyChart";
import type { PunchcardPayload } from "@shared/types/api";

interface PunchcardProps {
  data: PunchcardPayload | undefined;
  height?: number;
}

const DOW = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

// boom-k2p: right margin bumped to 18 so the rightmost 23:00 column's
// circle radius doesn't clip past the card's inner edge on narrower
// widget footprints. Left/bottom axis space unchanged.
const MARGIN = { top: 8, right: 18, bottom: 22, left: 34 };

/**
 * Classic 7x24 punchcard: rows = day of week (Sun..Sat), cols = hour (0..23),
 * bubble radius ∝ seconds. Times are UTC (backend aggregates in UTC); a small
 * note communicates that. Dark-mode native; responsive.
 */
export function Punchcard({ data, height = 260 }: PunchcardProps) {
  const surface = useD3Surface(
    { height, margin: MARGIN },
    ({ g, innerW, innerH, showTip, hideTip }) => {
      if (!data || data.cells.length === 0) return;

      const fg = cssVar("--muted-foreground");

      const x = d3.scaleBand<number>().domain(d3.range(24)).range([0, innerW]).padding(0.1);
      const y = d3.scaleBand<number>().domain(d3.range(7)).range([0, innerH]).padding(0.1);

      const maxSeconds = data.maxSeconds || d3.max(data.cells, (c) => c.seconds) || 1;
      const rMax = Math.min(x.bandwidth(), y.bandwidth()) / 2 - 1;
      const r = d3.scaleSqrt().domain([0, maxSeconds]).range([0, rMax]);

      // Hour axis (every 3h).
      styleAxis(
        g
          .append("g")
          .attr("transform", `translate(0,${innerH})`)
          .call(
            d3.axisBottom(x).tickValues(d3.range(0, 24, 3)).tickFormat((d) => String(d)),
          ),
        { fg },
        { fontSize: "10px" },
      );

      // Day-of-week axis.
      styleAxis(
        g.append("g").call(d3.axisLeft(y).tickFormat((d) => DOW[Number(d)])),
        { fg },
        { fontSize: "10px" },
      );

      const total = d3.sum(data.cells, (c) => c.seconds) || 1;
      const color = colorAt(0);

      // boom-9pt: rank across ACTIVE cells only. On the 7×24 grid most cells
      // are 0; ranking against all 168 would make even a top-3 cell look
      // unimportant ("#3 of 168"). Rank map by `${dow}-${hour}` key.
      const activeCells = data.cells.filter((c) => c.seconds > 0);
      const cellRank = new Map<string, number>();
      [...activeCells]
        .sort((a, b) => b.seconds - a.seconds)
        .forEach((c, i) => cellRank.set(`${c.dow}-${c.hour}`, i + 1));
      const rankBase = activeCells.length;

      g.selectAll("circle.punch")
        .data(activeCells)
        .join("circle")
        .attr("class", "punch")
        .attr("cx", (c) => (x(c.hour) ?? 0) + x.bandwidth() / 2)
        .attr("cy", (c) => (y(c.dow) ?? 0) + y.bandwidth() / 2)
        .attr("r", (c) => Math.max(1.5, r(c.seconds)))
        .attr("fill", color)
        .attr("fill-opacity", 0.85)
        .on("mousemove", (event, c) => {
          const nextH = (c.hour + 1) % 24;
          const rk = cellRank.get(`${c.dow}-${c.hour}`) ?? 0;
          const { rows, footer } = rankedContent(
            c.seconds,
            total,
            rk,
            rankBase,
            secondsToHms,
            { shareLabel: "Share of week" },
          );
          // Preserve the UTC context that already lived in the footer; chain
          // it with the rank string when rank is present.
          const combinedFooter = footer ? `${footer} · UTC` : "UTC";
          showTip(
            event,
            tooltipHtml({
              title: `${DOW[c.dow]} ${String(c.hour).padStart(2, "0")}:00–${String(nextH).padStart(2, "0")}:00`,
              titleSwatch: color,
              rows,
              footer: combinedFooter,
            }),
          );
        })
        .on("mouseleave", hideTip);
    },
    [data],
  );

  // A week of coding is the BASE CASE: a normal week maps dozens of the 168
  // day×hour cells, so the grid renders the moment there's ANY activity. We
  // deliberately do NOT gate on range width or a minimum cell/day count — the
  // punchcard is meant to read at a 1-week window. The only empty state is a
  // genuinely activity-free range, and its copy must NOT imply the window is
  // "too short" (widening isn't the fix — logging coding time is).
  const hasActivity =
    (data?.totalSeconds ?? 0) > 0 || (data?.cells.some((c) => c.seconds > 0) ?? false);
  if (!data || !hasActivity) {
    return (
      <EmptyChart
        height={height}
        title="No coding activity in this range"
        hint="The punchcard maps your day-of-week × hour rhythm — a single week of coding is enough to fill it in."
      />
    );
  }

  // boom-k2p: pull the "UTC" note out of the ChartSurface's fixed-height
  // container (was overflowing bottom on tight tiles) and pin it as a small
  // absolute badge in the corner. Still visible; no longer competes for
  // vertical space against the svg.
  return (
    <div className="relative h-full w-full">
      <ChartSurface surface={surface} />
      <span
        className="pointer-events-none absolute right-1 top-0 font-mono text-[9px] uppercase tracking-[0.14em] text-muted-foreground/70"
        aria-hidden
      >
        UTC
      </span>
    </div>
  );
}
