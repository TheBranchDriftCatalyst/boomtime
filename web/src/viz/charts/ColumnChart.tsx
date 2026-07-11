import * as d3 from "d3";
import { secondsToHms } from "@/lib/utils";
import { cssVar } from "@/viz/d3/useChartFrame";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";
import { tooltipHtml } from "@/viz/d3/tooltip";
import { fmtDateRange, fmtPct } from "@/viz/d3/tooltipContent";
import {
  formatDay,
  gridlines,
  hoursTickFormat,
  styleAxis,
  thinnedDateTicks,
} from "@/viz/d3/axes";
import { colorAt } from "@/viz/d3/color";
import { EmptyChart } from "@/viz/d3/EmptyChart";

// One stacked series (segment) of a stacked column chart. `values` is aligned
// to the chart's `dates`; `seconds` per bucket.
export interface ColumnSeries {
  name: string;
  values: number[];
  color: string;
}

interface ColumnChartBaseProps {
  // Parallel arrays: ISO dates and per-day totals in seconds.
  dates: string[];
  seriesName?: string;
  height?: number;
  /**
   * Optional per-bucket ranges (start/end ISO). When the caller has bucketed
   * daily data (weekly bars on long ranges) this makes tooltips read
   * "12–18 Jan 2026" instead of the misleading "12 Jan 2026" (bucket start).
   * Length must match `dates`; ignored otherwise.
   */
  ranges?: { start: string; end: string }[];
}

// Discriminated union: either single-series `values` bars, or a stacked
// column chart with one segment per `series` entry (series carries its own
// color so callers can share a palette across charts).
export type ColumnChartProps =
  | (ColumnChartBaseProps & { values: number[]; series?: undefined })
  | (ColumnChartBaseProps & { series: ColumnSeries[]; values?: undefined });

// Stable default so series-only call sites don't get a fresh array (and thus a
// full SVG teardown/redraw) on every parent render.
const EMPTY: number[] = [];

const MARGIN = { top: 10, right: 12, bottom: 28, left: 44 };

/** D3 1:1 port of the daily-activity column chart, with an optional stacked
 * (multi-series) mode driven by the `series` prop. */
export function ColumnChart(props: ColumnChartProps) {
  const { dates, seriesName = "Coding time", height = 320, series, ranges } = props;
  const values = props.values ?? EMPTY;

  // Choose the tooltip's date/date-range subtitle for a given index. When
  // ranges are supplied and > 1 day, format as a compact range; otherwise
  // fall back to the bucket's representative day.
  const subtitleFor = (i: number, d: Date): string => {
    const r = ranges?.[i];
    if (r && r.start && r.end && r.start !== r.end) {
      return fmtDateRange(r.start, r.end);
    }
    return formatDay(d);
  };

  const surface = useD3Surface(
    { height, margin: MARGIN },
    ({ g, innerW, innerH, showTip, hideTip }) => {
      const stacked = series !== undefined && series.length > 0;
      // Per-day total (matches the single-series total when stacked, so nothing
      // regresses vs the old flat column of daily totals).
      const totals = dates.map((_d, i) =>
        stacked
          ? series.reduce((s, ser) => s + (ser.values[i] ?? 0), 0)
          : values[i] ?? 0,
      );
      const data = dates.map((d, i) => ({ date: new Date(d), y: totals[i] }));

      const fg = cssVar("--muted-foreground");
      const border = cssVar("--border");
      const card = cssVar("--card");

      const x = d3
        .scaleBand<Date>()
        .domain(data.map((d) => d.date))
        .range([0, innerW])
        .padding(0.55);

      const yMax = d3.max(data, (d) => d.y) ?? 0;
      const y = d3.scaleLinear().domain([0, yMax || 1]).nice().range([innerH, 0]);

      gridlines(g, y, { span: innerW, stroke: border });

      // Y axis (hours).
      styleAxis(
        g.append("g").call(d3.axisLeft(y).ticks(5).tickFormat(hoursTickFormat())),
        { fg },
      );

      // Y axis title.
      g.append("text")
        .attr("transform", "rotate(-90)")
        .attr("x", -innerH / 2)
        .attr("y", -MARGIN.left + 12)
        .attr("text-anchor", "middle")
        .attr("fill", fg)
        .style("font-size", "11px")
        .text("Hours");

      // X axis (dates) — thin out ticks to avoid clutter.
      styleAxis(
        g
          .append("g")
          .attr("transform", `translate(0,${innerH})`)
          .call(
            d3
              .axisBottom(x)
              .tickValues(thinnedDateTicks(data.map((d) => d.date)))
              .tickFormat((d) => formatDay(d as Date)),
          ),
        { fg, border },
        { domain: "line" },
      );

      if (stacked) {
        // One <g> per date holding stacked segments; tooltip shows category+hours.
        const dateGroups = g
          .selectAll("g.col")
          .data(dates.map((d, i) => ({ date: new Date(d), i })))
          .join("g")
          .attr("class", "col")
          .attr("transform", (d) => `translate(${x(d.date) ?? 0},0)`);

        dateGroups.each(function (col) {
          const cell = d3.select(this);
          const colTotal = totals[col.i] || 0;
          let acc = 0; // running seconds from the bottom of the bar
          for (const ser of series) {
            const v = ser.values[col.i] ?? 0;
            if (v <= 0) continue;
            const y0 = acc;
            const y1 = acc + v;
            acc = y1;
            const share = colTotal > 0 ? (v / colTotal) * 100 : 0;
            cell
              .append("rect")
              .attr("x", 0)
              .attr("width", x.bandwidth())
              .attr("y", y(y1))
              .attr("height", Math.max(0, y(y0) - y(y1)))
              .attr("fill", ser.color)
              .attr("stroke", card)
              .attr("stroke-width", 0.5)
              .on("mousemove", (event) => {
                showTip(
                  event,
                  tooltipHtml({
                    title: ser.name,
                    titleSwatch: ser.color,
                    subtitle: subtitleFor(col.i, col.date),
                    rows: [
                      { label: "Time", value: secondsToHms(v) },
                      { label: "Share of day", value: fmtPct(share) },
                      { label: "Day total", value: secondsToHms(colTotal), muted: true },
                    ],
                  }),
                );
              })
              .on("mouseleave", hideTip);
          }
        });
      } else {
        g.selectAll("rect.bar")
          .data(data)
          .join("rect")
          .attr("class", "bar")
          .attr("x", (d) => x(d.date) ?? 0)
          .attr("width", x.bandwidth())
          .attr("y", (d) => y(d.y))
          .attr("height", (d) => innerH - y(d.y))
          .attr("rx", 4)
          .attr("fill", colorAt(0))
          .on("mousemove", (event, d) => {
            const i = data.indexOf(d);
            const rng = i >= 0 ? ranges?.[i] : undefined;
            const subtitle =
              rng && rng.start && rng.end && rng.start !== rng.end
                ? fmtDateRange(rng.start, rng.end)
                : undefined;
            showTip(
              event,
              tooltipHtml({
                title: formatDay(d.date),
                subtitle,
                rows: [{ label: seriesName, value: secondsToHms(d.y) }],
              }),
            );
          })
          .on("mouseleave", hideTip);
      }
    },
    [dates, values, series, seriesName, ranges],
  );

  if (dates.length === 0) return <EmptyChart height={height} />;

  return <ChartSurface surface={surface} />;
}
