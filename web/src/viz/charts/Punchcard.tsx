import * as d3 from "d3";
import { secondsToHms } from "@/lib/utils";
import { cssVar } from "@/viz/d3/useChartFrame";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";
import { tooltipHtml } from "@/viz/d3/tooltip";
import { fmtPct } from "@/viz/d3/tooltipContent";
import { styleAxis } from "@/viz/d3/axes";
import { colorAt } from "@/viz/d3/color";
import { EmptyChart } from "@/viz/d3/EmptyChart";
import type { PunchcardPayload } from "@/types/api";

interface PunchcardProps {
  data: PunchcardPayload | undefined;
  height?: number;
}

const DOW = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

const MARGIN = { top: 8, right: 12, bottom: 22, left: 34 };

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

      g.selectAll("circle.punch")
        .data(data.cells.filter((c) => c.seconds > 0))
        .join("circle")
        .attr("class", "punch")
        .attr("cx", (c) => (x(c.hour) ?? 0) + x.bandwidth() / 2)
        .attr("cy", (c) => (y(c.dow) ?? 0) + y.bandwidth() / 2)
        .attr("r", (c) => Math.max(1.5, r(c.seconds)))
        .attr("fill", color)
        .attr("fill-opacity", 0.85)
        .on("mousemove", (event, c) => {
          const share = (c.seconds / total) * 100;
          const nextH = (c.hour + 1) % 24;
          showTip(
            event,
            tooltipHtml({
              title: `${DOW[c.dow]} ${String(c.hour).padStart(2, "0")}:00–${String(nextH).padStart(2, "0")}:00`,
              titleSwatch: color,
              rows: [
                { label: "Time", value: secondsToHms(c.seconds) },
                { label: "Share of week", value: fmtPct(share) },
              ],
              footer: "UTC",
            }),
          );
        })
        .on("mouseleave", hideTip);
    },
    [data],
  );

  // Distinguish three states so the empty-state is actionable:
  //   1. No payload / zero cells → generic "no data" placeholder.
  //   2. Cells present but no non-zero seconds → range yielded no
  //      punchcard-eligible activity (e.g. very narrow range on a quiet
  //      week); hint the user to widen the range instead of squinting at an
  //      empty grid where every dot has radius 0.
  //   3. Otherwise render the chart.
  if (!data || data.cells.length === 0) {
    return (
      <EmptyChart
        height={height}
        title="No punchcard data for this range"
        hint="Widen the date range in the toolbar to see day-of-week × hour activity."
      />
    );
  }
  const hasActivity =
    (data.totalSeconds ?? 0) > 0 || data.cells.some((c) => c.seconds > 0);
  if (!hasActivity) {
    return (
      <EmptyChart
        height={height}
        title="No punchcard-eligible activity in this range"
        hint="Try a wider date range in the toolbar — the current range has no coding hours to plot."
      />
    );
  }

  return (
    <ChartSurface surface={surface}>
      <p className="mt-1 text-xs text-muted-foreground">Times shown in UTC.</p>
    </ChartSurface>
  );
}
