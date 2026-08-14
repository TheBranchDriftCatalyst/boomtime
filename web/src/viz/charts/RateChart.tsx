// RateChart — a GENERIC "rate over time" area+line chart for one metric series
// (gaka-metrics). It renders ANY internal/metrics series: counter series show a
// per-minute event rate, gauge series show the last observed value per minute.
// New backend metrics need ZERO frontend work — the Metrics tab maps each
// series straight onto this component.
//
// Built on the shared useD3Surface primitive (same ritual every viz chart uses:
// measured frame, tooltip lifecycle, theme-aware redraw) — no hand-rolled SVG.
// Compact by default so a grid of these reads like a wall of sparklines with
// axes.
import { useMemo } from "react";
import * as d3 from "d3";
import type { MetricSeries } from "@/types/api";
import { cssVar } from "@/viz/d3/useChartFrame";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";
import { tooltipHtml } from "@/viz/d3/tooltip";
import { gridlines, styleAxis, thinnedDateTicks } from "@/viz/d3/axes";
import { colorAt } from "@/viz/d3/color";
import { EmptyChart } from "@/viz/d3/EmptyChart";

interface RateChartProps {
  series: MetricSeries;
  height?: number;
  /** Palette index so sibling charts in a group get distinct hues. */
  colorIndex?: number;
}

const MARGIN = { top: 8, right: 12, bottom: 22, left: 36 };

// hh:mm for a bucket tick — the window is ~2h so day/date is noise.
const fmtClock = d3.timeFormat("%H:%M");

/** Render one metric series as a rate-over-time area + line. */
export function RateChart({ series, height = 120, colorIndex = 0 }: RateChartProps) {
  const data = useMemo(
    () =>
      (series.points ?? []).map((p, i) => ({
        date: new Date(p.bucket),
        value: p.value,
        i,
      })),
    [series.points],
  );

  const isGauge = series.kind === "gauge";

  const surface = useD3Surface(
    { height, margin: MARGIN },
    ({ g, innerW, innerH, showTip, hideTip }) => {
      if (data.length === 0) return;

      const fg = cssVar("--muted-foreground");
      const border = cssVar("--border");
      const color = colorAt(colorIndex);

      const x = d3
        .scaleTime()
        .domain(d3.extent(data, (d) => d.date) as [Date, Date])
        .range([0, innerW]);
      const yMax = d3.max(data, (d) => d.value) ?? 0;
      const y = d3
        .scaleLinear()
        .domain([0, yMax || 1])
        .nice()
        .range([innerH, 0]);

      gridlines(g, y, { span: innerW, stroke: border });

      styleAxis(
        g.append("g").call(
          d3
            .axisLeft(y)
            .ticks(3)
            .tickFormat((v) => String(+v)),
        ),
        { fg },
      );
      styleAxis(
        g
          .append("g")
          .attr("transform", `translate(0,${innerH})`)
          .call(
            d3
              .axisBottom(x)
              .tickValues(
                thinnedDateTicks(
                  data.map((d) => d.date),
                  Math.max(2, Math.floor(innerW / 72)),
                ),
              )
              .tickFormat((d) => fmtClock(d as Date)),
          ),
        { fg, border },
        { domain: "line" },
      );

      // Gauge: a value the eye reads as a level → step curve, no fill. Counter:
      // an event rate → smooth filled area, the classic "rate over time" look.
      const curve = isGauge ? d3.curveStepAfter : d3.curveMonotoneX;

      if (!isGauge) {
        const area = d3
          .area<(typeof data)[number]>()
          .x((d) => x(d.date))
          .y0(innerH)
          .y1((d) => y(d.value))
          .curve(curve);
        g.append("path")
          .datum(data)
          .attr("d", area)
          .attr("fill", color)
          .attr("fill-opacity", 0.16);
      }

      const line = d3
        .line<(typeof data)[number]>()
        .x((d) => x(d.date))
        .y((d) => y(d.value))
        .curve(curve);
      g.append("path")
        .datum(data)
        .attr("d", line)
        .attr("fill", "none")
        .attr("stroke", color)
        .attr("stroke-width", 1.75);

      // Invisible fat hover targets over each bucket.
      const unit = series.unit ? ` ${series.unit}` : "";
      const valueLabel = isGauge ? "Value" : "Rate";
      const suffix = isGauge ? unit : `${unit}/min`;
      g.selectAll("circle.pt")
        .data(data)
        .join("circle")
        .attr("class", "pt")
        .attr("cx", (d) => x(d.date))
        .attr("cy", (d) => y(d.value))
        .attr("r", 7)
        .attr("fill", "transparent")
        .on("mousemove", (event, d) => {
          showTip(
            event,
            tooltipHtml({
              title: fmtClock(d.date),
              titleSwatch: color,
              rows: [
                {
                  label: valueLabel,
                  value: `${(+d.value).toLocaleString()}${suffix}`,
                },
              ],
            }),
          );
        })
        .on("mouseleave", hideTip);
    },
    [data, isGauge, colorIndex, series.unit],
  );

  if (data.length === 0) return <EmptyChart height={height} />;

  return <ChartSurface surface={surface} />;
}

// currentRate returns the value of a series' most recent bucket (the "now"
// rate), or 0 when the series has no points. Exposed so the tab can show a
// headline number beside each chart without re-deriving it.
export function currentRate(series: MetricSeries): number {
  const pts = series.points;
  if (!pts || pts.length === 0) return 0;
  return pts[pts.length - 1]?.value ?? 0;
}
