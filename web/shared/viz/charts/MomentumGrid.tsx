import { useMemo } from "react";
import * as d3 from "d3";
import { secondsToCompact, secondsToHms, truncate } from "@shared/lib/utils";
import { cssVar } from "@shared/viz/d3/useChartFrame";
import { useD3Surface } from "@shared/viz/d3/useD3Surface";
import { ChartSurface } from "@shared/viz/d3/ChartSurface";
import { tooltipHtml } from "@shared/viz/d3/tooltip";
import { fmtDelta, fmtPct } from "@shared/viz/d3/tooltipContent";
import { adaptiveDateFormatter, styleAxis, thinnedDateTicks } from "@shared/viz/d3/axes";
import { emptyFloor } from "@shared/viz/d3/color";
import { EmptyChart } from "@shared/viz/d3/EmptyChart";
import type { MomentumPayload } from "@shared/types/api";

interface MomentumGridProps {
  data: MomentumPayload | undefined;
  rowHeight?: number;
  // boom-nmk: render each active cell's value (compact hrs/min) centered in the
  // rect. Self-suppressing — a cell only gets a label when it's big enough for
  // the number to read (see MIN_CELL_W/H), so narrow week columns stay clean.
  // Defaults on; pass false to force the plain heatmap.
  showValues?: boolean;
}

const MARGIN = { top: 6, right: 8, bottom: 26, left: 110 };

// boom-nmk: min cell footprint for an in-cell value label. Below either bound
// the compact duration ("2h 5m") would clip or crowd, so we skip it and leave
// the tooltip to carry the exact value.
const MIN_CELL_W = 40;
const MIN_CELL_H = 16;

/**
 * Project x week momentum heatmap: rows = top projects, cols = weeks, cell
 * intensity ∝ seconds (per-row scale so each project's ramp shows). Reveals
 * which projects are heating up / cooling down. Row label + hover.
 */
export function MomentumGrid({ data, rowHeight = 26, showValues = true }: MomentumGridProps) {
  const rows = useMemo(() => data?.projects ?? [], [data]);
  const weeks = useMemo(() => data?.weeks ?? [], [data]);
  const height = Math.max(120, rows.length * rowHeight + 40);

  const surface = useD3Surface(
    { height, margin: MARGIN },
    ({ g, innerW, innerH, showTip, hideTip }) => {
      if (rows.length === 0 || weeks.length === 0) return;

      const fg = cssVar("--muted-foreground");
      const base = cssVar("--primary");
      const floor = emptyFloor();

      const x = d3.scaleBand<number>().domain(d3.range(weeks.length)).range([0, innerW]).padding(0.08);
      const y = d3.scaleBand<string>().domain(rows.map((r) => r.name)).range([0, innerH]).padding(0.12);

      // Per-row opacity ramp over --primary so even small weeks are visible (0 =>
      // empty floor; smallest active => ~0.2; row max => 1.0). Using opacity of a
      // solid primary fill avoids interpolating the oklch theme tokens (which
      // d3.interpolateRgb can't parse and would collapse to black).
      const rowOpacity = new Map<string, (v: number) => number>();
      for (const p of rows) {
        const max = d3.max(p.weekly) ?? 0;
        const scale = d3
          .scaleLinear()
          .domain([0, max || 1])
          .range([0.2, 1])
          .clamp(true);
        rowOpacity.set(p.name, (v: number) => (v <= 0 ? 0 : scale(v)));
      }

      // Row labels (project names, truncated; full name on hover).
      styleAxis(
        g
          .append("g")
          .call(d3.axisLeft(y).tickSize(0).tickFormat((d) => truncate(String(d), 14))),
        { fg },
      )
        .selectAll<SVGTextElement, string>("text")
        .append("title")
        .text((d) => String(d));

      // Week axis (thinned to ~8 labels). Formatter is span-adaptive so a
      // multi-year range doesn't render as "26 Dec, 27 Apr, 27 Aug" (which
      // reads as ambiguous day-of-month ticks) — instead you get
      // "Dec '24, Apr '25, Aug '25" and the axis is honest.
      const weekDates = weeks.map((w) => new Date(w));
      const fmt = adaptiveDateFormatter(weekDates);
      styleAxis(
        g
          .append("g")
          .attr("transform", `translate(0,${innerH})`)
          .call(
            d3
              .axisBottom(x)
              .tickValues(thinnedDateTicks(d3.range(weeks.length)))
              .tickFormat((i) => fmt(weekDates[Number(i)])),
          ),
        { fg },
        { fontSize: "10px" },
      );

      const cells: { project: string; wi: number; seconds: number }[] = [];
      for (const p of rows) {
        weeks.forEach((_w, wi) => {
          cells.push({ project: p.name, wi, seconds: p.weekly[wi] ?? 0 });
        });
      }

      // Empty-floor background so the grid reads even where activity is 0.
      g.selectAll("rect.floor")
        .data(cells)
        .join("rect")
        .attr("class", "floor")
        .attr("x", (c) => x(c.wi) ?? 0)
        .attr("y", (c) => y(c.project) ?? 0)
        .attr("width", x.bandwidth())
        .attr("height", y.bandwidth())
        .attr("rx", 2)
        .attr("fill", floor);

      // Per-project weekly max — used for "share of project peak" context.
      const projMax = new Map<string, number>();
      for (const p of rows) projMax.set(p.name, d3.max(p.weekly) ?? 0);

      g.selectAll("rect.cell")
        .data(cells)
        .join("rect")
        .attr("class", "cell")
        .attr("x", (c) => x(c.wi) ?? 0)
        .attr("y", (c) => y(c.project) ?? 0)
        .attr("width", x.bandwidth())
        .attr("height", y.bandwidth())
        .attr("rx", 2)
        .attr("fill", base)
        .attr("fill-opacity", (c) => rowOpacity.get(c.project)!(c.seconds))
        .on("mousemove", (event, c) => {
          // Week starts covered by this cell. Ranges implied from adjacent
          // weeks (weeks[i+1] - 1 day) would over-specify — the week label is
          // enough context here.
          const weekLabel = d3.timeFormat("%d %b %Y")(new Date(weeks[c.wi]));
          const proj = rows.find((r) => r.name === c.project);
          const prev = c.wi > 0 ? proj?.weekly[c.wi - 1] ?? 0 : 0;
          const projPeak = projMax.get(c.project) ?? 0;
          const shareOfPeak =
            projPeak > 0 ? (c.seconds / projPeak) * 100 : 0;
          const rows0: { label: string; value: string; muted?: boolean }[] = [
            {
              label: "Time",
              value: c.seconds > 0 ? secondsToHms(c.seconds) : "0",
            },
          ];
          if (c.seconds > 0)
            rows0.push({
              label: "Share of peak",
              value: fmtPct(shareOfPeak),
              muted: true,
            });
          const delta = c.wi > 0 ? fmtDelta(c.seconds, prev) : "";
          showTip(
            event,
            tooltipHtml({
              title: c.project,
              titleSwatch: base,
              subtitle: `Week of ${weekLabel}`,
              rows: rows0,
              footer: delta || undefined,
            }),
          );
        })
        .on("mouseleave", hideTip);

      // boom-nmk: in-cell value labels. Only when opted in AND the cell is large
      // enough for the compact duration to read — narrow week columns get no
      // label (the tooltip still carries the exact value). Light glyph + dark
      // halo so it reads over the full primary-opacity ramp underneath.
      if (
        showValues &&
        x.bandwidth() >= MIN_CELL_W &&
        y.bandwidth() >= MIN_CELL_H
      ) {
        g.selectAll("text.cell-value")
          .data(cells.filter((c) => c.seconds > 0))
          .join("text")
          .attr("class", "cell-value")
          .attr("x", (c) => (x(c.wi) ?? 0) + x.bandwidth() / 2)
          .attr("y", (c) => (y(c.project) ?? 0) + y.bandwidth() / 2)
          .attr("text-anchor", "middle")
          .attr("dominant-baseline", "central")
          .style("font-size", "9px")
          .style("font-family", "var(--font-mono, ui-monospace, monospace)")
          .style("paint-order", "stroke")
          .style("stroke", "rgba(0, 0, 0, 0.7)")
          .style("stroke-width", "2px")
          .style("stroke-linejoin", "round")
          .attr("fill", "rgba(255, 255, 255, 0.92)")
          .style("pointer-events", "none")
          .text((c) => secondsToCompact(c.seconds));
      }
    },
    [rows, weeks, showValues],
  );

  // Momentum buckets by ISO week, so ranges shorter than ~2 weeks collapse to
  // a single column and there's no ramp to see. Communicate this instead of
  // showing a lone strip that reads as "empty".
  if (rows.length === 0 || weeks.length === 0) {
    return (
      <EmptyChart
        height={140}
        title="No momentum data for this range"
        hint="Widen the date range in the toolbar — momentum needs multiple weeks of activity to plot."
      />
    );
  }
  if (weeks.length < 2) {
    return (
      <EmptyChart
        height={140}
        title="Range too short for weekly buckets"
        hint="Try 30+ days — momentum shows how projects heat up or cool down week over week."
      />
    );
  }

  return <ChartSurface surface={surface} />;
}
