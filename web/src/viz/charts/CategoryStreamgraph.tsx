import { useMemo } from "react";
import * as d3 from "d3";
import { secondsToHms } from "@/lib/utils";
import { cssVar } from "@/viz/d3/useChartFrame";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";
import { tooltipHtml } from "@/viz/d3/tooltip";
import { fmtDateRange, fmtPct } from "@/viz/d3/tooltipContent";
import { formatDay, styleAxis, thinnedDateTicks } from "@/viz/d3/axes";
import { orderCategories, paletteByName } from "@/viz/d3/color";
import { renderLegend } from "@/viz/d3/legend";
import { EmptyChart } from "@/viz/d3/EmptyChart";
import type { ResourceStats } from "@/types/api";

interface CategoryStreamgraphProps {
  // Category resources with a per-bucket `totalDaily` series (already bucketed
  // by the caller) aligned to `dates`.
  categories: ResourceStats[];
  dates: string[];
  height?: number;
  /** Optional per-bucket ranges — tooltips read as "12–18 Jan 2026". */
  ranges?: { start: string; end: string }[];
}

const LEGEND_H = 22;
const MARGIN = { top: LEGEND_H + 4, right: 12, bottom: 24, left: 12 };

/**
 * Streamgraph (stacked area with wiggle/silhouette offset) of category time
 * over the bucketed day series — the "what kind of work" narrative. Legend +
 * hover. Dark-mode native; responsive.
 */
export function CategoryStreamgraph({
  categories,
  dates,
  height = 320,
  ranges,
}: CategoryStreamgraphProps) {
  // Shared category ordering (real categories by total desc, "Other (…)" last)
  // — the SAME contract OverviewDashboard's stacked columns replay.
  const series = useMemo(() => orderCategories(categories), [categories]);

  const surface = useD3Surface(
    { height, margin: MARGIN },
    ({ svg, g, innerW, innerH, showTip, hideTip }) => {
      if (series.length === 0 || dates.length === 0) return;

      const fg = cssVar("--muted-foreground");
      const card = cssVar("--card");

      // Build one row per bucket: { i, <cat>: seconds }.
      const keys = series.map((s) => s.name);
      const rows = dates.map((_d, i) => {
        const row: Record<string, number> = { i };
        for (const s of series) row[s.name] = s.totalDaily[i] ?? 0;
        return row;
      });

      const stack = d3
        .stack<Record<string, number>>()
        .keys(keys)
        .offset(d3.stackOffsetWiggle)
        .order(d3.stackOrderInsideOut);
      const stacked = stack(rows);

      const x = d3
        .scaleLinear()
        .domain([0, dates.length - 1])
        .range([0, innerW]);
      const yMin = d3.min(stacked, (layer) => d3.min(layer, (d) => d[0])) ?? 0;
      const yMax = d3.max(stacked, (layer) => d3.max(layer, (d) => d[1])) ?? 1;
      const y = d3.scaleLinear().domain([yMin, yMax]).range([innerH, 0]);

      // Positional palette over the ordered series (shared contract).
      const palette = paletteByName(series);

      const area = d3
        .area<d3.SeriesPoint<Record<string, number>>>()
        .x((d) => x(d.data.i))
        .y0((d) => y(d[0]))
        .y1((d) => y(d[1]))
        .curve(d3.curveBasis);

      g.selectAll("path.layer")
        .data(stacked)
        .join("path")
        .attr("class", "layer")
        .attr("d", area)
        .attr("fill", (layer) => palette.get(layer.key)!)
        .attr("stroke", card)
        .attr("stroke-width", 0.5)
        .on("mousemove", (event, layer) => {
          // Invert x -> bucket index (round; curveBasis smoothing means the
          // visual band at cursor can differ slightly from the bucket value —
          // acceptable per DESIGN, but round rather than interpolate).
          const [mx] = d3.pointer(event, g.node() as SVGGElement);
          const rawIdx = x.invert(mx);
          const i = Math.max(0, Math.min(dates.length - 1, Math.round(rawIdx)));
          const bucketVal =
            series.find((s) => s.name === layer.key)?.totalDaily[i] ?? 0;
          const bucketTotal = series.reduce(
            (s, ser) => s + (ser.totalDaily[i] ?? 0),
            0,
          );
          const share = bucketTotal > 0 ? (bucketVal / bucketTotal) * 100 : 0;
          const rng = ranges?.[i];
          const subtitle =
            rng && rng.start && rng.end && rng.start !== rng.end
              ? fmtDateRange(rng.start, rng.end)
              : formatDay(new Date(dates[i]));
          showTip(
            event,
            tooltipHtml({
              title: layer.key,
              titleSwatch: palette.get(layer.key)!,
              subtitle,
              rows: [
                { label: "Time", value: secondsToHms(bucketVal) },
                { label: "Share of bucket", value: fmtPct(share) },
              ],
            }),
          );
        })
        .on("mouseleave", hideTip);

      // X axis (dates, thinned).
      styleAxis(
        g
          .append("g")
          .attr("transform", `translate(0,${innerH})`)
          .call(
            d3
              .axisBottom(x)
              .tickValues(thinnedDateTicks(d3.range(dates.length)))
              .tickFormat((i) => formatDay(new Date(dates[Number(i)]))),
          ),
        { fg },
        { fontSize: "10px" },
      );

      // Legend across the top, overflow collapsed to "+N more".
      renderLegend(
        svg,
        keys.map((k) => ({ label: k, color: palette.get(k)! })),
        { x: MARGIN.left, y: 2, fg, maxWidth: innerW, gap: 28 },
      );
    },
    [series, dates, ranges],
  );

  if (series.length === 0) return <EmptyChart height={height} />;

  return <ChartSurface surface={surface} />;
}
