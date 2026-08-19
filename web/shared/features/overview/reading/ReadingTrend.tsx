// ReadingTrend — a small multi-series area+line over time, used by the
// "Listening trend (12 weeks)" tile with an optional coding overlay for the
// reading-vs-coding story. Purpose-built (the Overview's CumulativeArea is a
// single cumulative running-total; this is per-bucket, multi-series, seconds).
//
// Each point is rendered as a real <circle data-testid="trend-point"> so the
// tile's test can assert the mapped series lengths without SVG path math, and
// the legend names the series.
import { useMemo } from "react";
import * as d3 from "d3";
import { EmptyChart } from "@shared/viz/d3/EmptyChart";
import type { SeriesPoint } from "@shared/lib/queryApi";

export interface TrendSeries {
  name: string;
  color: string;
  points: SeriesPoint[];
}

interface ReadingTrendProps {
  series: TrendSeries[];
  height?: number;
  /** Value formatter for the hover title (seconds → "3h 12m"). */
  valueFmt: (v: number) => string;
  emptyHint?: string;
}

const MARGIN = { top: 10, right: 12, bottom: 22, left: 12 };
const VIEW_W = 640;

export function ReadingTrend({
  series,
  height = 260,
  valueFmt,
  emptyHint,
}: ReadingTrendProps) {
  const nonEmpty = useMemo(
    () => series.filter((s) => s.points.length > 0),
    [series],
  );

  const model = useMemo(() => {
    if (nonEmpty.length === 0) return null;
    const innerW = VIEW_W - MARGIN.left - MARGIN.right;
    const innerH = height - MARGIN.top - MARGIN.bottom;

    const allDates = nonEmpty.flatMap((s) => s.points.map((p) => new Date(p.bucket)));
    const xDomain = d3.extent(allDates) as [Date, Date];
    const x = d3.scaleTime().domain(xDomain).range([0, innerW]);
    const yMax = d3.max(nonEmpty.flatMap((s) => s.points.map((p) => p.value))) ?? 0;
    const y = d3.scaleLinear().domain([0, yMax || 1]).nice().range([innerH, 0]);

    const line = d3
      .line<SeriesPoint>()
      .x((p) => x(new Date(p.bucket)))
      .y((p) => y(p.value))
      .curve(d3.curveMonotoneX);
    const area = d3
      .area<SeriesPoint>()
      .x((p) => x(new Date(p.bucket)))
      .y0(innerH)
      .y1((p) => y(p.value))
      .curve(d3.curveMonotoneX);

    return {
      innerH,
      x,
      y,
      shapes: nonEmpty.map((s) => ({
        ...s,
        linePath: line(s.points) ?? "",
        areaPath: area(s.points) ?? "",
        dots: s.points.map((p) => ({
          cx: x(new Date(p.bucket)),
          cy: y(p.value),
          value: p.value,
          bucket: p.bucket,
        })),
      })),
    };
  }, [nonEmpty, height]);

  if (!model) {
    return <EmptyChart height={height} hint={emptyHint} />;
  }

  return (
    <div data-testid="reading-trend">
      <svg
        viewBox={`0 0 ${VIEW_W} ${height}`}
        width="100%"
        height={height}
        preserveAspectRatio="none"
        role="img"
        aria-label="Listening trend over time"
      >
        <g transform={`translate(${MARGIN.left}, ${MARGIN.top})`}>
          {/* baseline */}
          <line
            x1={0}
            x2={VIEW_W - MARGIN.left - MARGIN.right}
            y1={model.innerH}
            y2={model.innerH}
            stroke="hsl(var(--border))"
            strokeWidth={1}
          />
          {model.shapes.map((s) => (
            <g key={s.name} data-testid="trend-series" data-series={s.name}>
              <path d={s.areaPath} fill={s.color} fillOpacity={0.14} />
              <path
                d={s.linePath}
                fill="none"
                stroke={s.color}
                strokeWidth={2}
                style={{ filter: `drop-shadow(0 0 4px ${s.color})` }}
              />
              {s.dots.map((dot, i) => (
                <circle
                  key={i}
                  data-testid="trend-point"
                  cx={dot.cx}
                  cy={dot.cy}
                  r={2.5}
                  fill={s.color}
                >
                  <title>{valueFmt(dot.value)}</title>
                </circle>
              ))}
            </g>
          ))}
        </g>
      </svg>
      <ul className="mt-2 flex flex-wrap gap-x-4 gap-y-1" data-testid="reading-trend-legend">
        {model.shapes.map((s) => (
          <li key={s.name} className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <span
              aria-hidden
              className="h-2 w-2 rounded-full"
              style={{ background: s.color }}
            />
            {s.name}
          </li>
        ))}
      </ul>
    </div>
  );
}
