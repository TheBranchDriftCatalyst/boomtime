// CodingProjectsDonut — a donut + legend for the coding "Project breakdown"
// tile, driven by grouped seconds from the query DSL ({key,value}[] where value
// is attributed SECONDS). It is the coding-domain sibling of the reading
// ReadingDonut: same donut+legend shape and the same positional `colorAt`
// palette, but its legend values are formatted as durations (secondsToHms)
// rather than raw counts, and each legend row (except the "Other" roll-up)
// carries a canonical-pin toggle.
//
// The legend is real DOM text (project · time · share), so the tile's test can
// assert the mapped groups — and that a pinned low-share project renders as its
// own row/slice — without reaching into SVG geometry.
import { useMemo } from "react";
import * as d3 from "d3";
import { colorAt } from "@/viz/d3/color";
import { EmptyChart } from "@/viz/d3/EmptyChart";
import { secondsToHms } from "@/lib/utils";
import type { GroupRow } from "@/lib/queryApi";
import { PinToggle } from "@/features/pins/PinToggle";

interface CodingProjectsDonutProps {
  data: GroupRow[];
  height?: number;
  emptyHint?: string;
  // When set, each legend row (except the "Other" roll-up) gets a pin toggle
  // that pins/unpins that value on this axis (e.g. "project"). Omit to render a
  // plain, non-interactive legend.
  pinAxis?: string;
}

// The backend's bucket roll-up sentinel — never pinnable (it's not a real value).
const OTHER_KEY = "Other";

export function CodingProjectsDonut({
  data,
  height = 320,
  emptyHint,
  pinAxis,
}: CodingProjectsDonutProps) {
  const rows = useMemo(() => data.filter((d) => d.value > 0), [data]);

  const arcs = useMemo(() => {
    if (rows.length === 0) return [];
    const r = height / 2;
    const layout = d3
      .pie<GroupRow>()
      .sort(null)
      .value((d) => d.value);
    const gen = d3
      .arc<d3.PieArcDatum<GroupRow>>()
      .innerRadius(r * 0.58)
      .outerRadius(r * 0.98);
    return layout(rows).map((a, i) => ({
      d: gen(a) ?? "",
      color: colorAt(i),
      key: a.data.key,
      value: a.data.value,
    }));
  }, [rows, height]);

  if (rows.length === 0) {
    return <EmptyChart height={height} hint={emptyHint} />;
  }

  const total = rows.reduce((s, d) => s + d.value, 0) || 1;

  return (
    <div
      className="flex flex-wrap items-center gap-6"
      data-testid="coding-projects-donut"
    >
      <svg
        viewBox={`0 0 ${height} ${height}`}
        width={height}
        height={height}
        role="img"
        aria-label="Project breakdown"
        className="max-w-full shrink-0"
      >
        <g transform={`translate(${height / 2}, ${height / 2})`}>
          {arcs.map((a) => (
            <path
              key={a.key}
              d={a.d}
              fill={a.color}
              stroke="hsl(var(--card))"
              strokeWidth={2}
            >
              <title>
                {a.key}: {secondsToHms(a.value)} (
                {Math.round((a.value / total) * 100)}%)
              </title>
            </path>
          ))}
        </g>
      </svg>
      <ul
        className="flex min-w-[8rem] flex-1 flex-col gap-1.5"
        data-testid="coding-projects-legend"
      >
        {arcs.map((a) => (
          <li key={a.key} className="flex items-center gap-2 text-sm">
            <span
              aria-hidden
              className="h-2.5 w-2.5 shrink-0 rounded-full"
              style={{ background: a.color }}
            />
            <span className="truncate text-foreground/90" title={a.key}>
              {a.key}
            </span>
            <span className="ml-auto shrink-0 tabular-nums text-muted-foreground">
              {secondsToHms(a.value)}
            </span>
            {pinAxis && a.key !== OTHER_KEY && (
              <PinToggle axis={pinAxis} value={a.key} className="-my-1" />
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

export default CodingProjectsDonut;
