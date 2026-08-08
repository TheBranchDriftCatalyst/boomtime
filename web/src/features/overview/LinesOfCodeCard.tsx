// LinesOfCodeCard (gaka-yfg) — the Lines-of-Code Overview widget. Self-fetches
// via useOverviewLoc (shared OverviewDataContext range/space), so it drops into
// BOTH the legacy OverviewDashboard ChartCard stack and the composable widget
// grid with no props. Renders a headline Total LOC numeral, a total-LOC-over-
// time area, and a per-project breakdown bar list.
//
// LOC accent is deliberately VIOLET (#b967ff) — distinct from the GitHub
// contribution green (#39d353) used elsewhere on the Overview — with a cyan
// (#05d9e8) secondary for the per-project bars. Both are synthwave theme hues
// (CHART_COLORS), so the widget reads as part of the dashboard, not a bolt-on.
//
// Degrades gently: while loading it shows a muted "Measuring…" note; when the
// range has no file_lines data at all it shows a one-line hint (not an error)
// and never blocks the Overview.
import { useMemo } from "react";
import * as d3 from "d3";
import { Code2 } from "lucide-react";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";
import { cssVar } from "@/viz/d3/useChartFrame";
import { tooltipHtml } from "@/viz/d3/tooltip";
import {
  formatDay,
  gridlines,
  styleAxis,
  thinnedDateTicks,
} from "@/viz/d3/axes";
import { EmptyChart } from "@/viz/d3/EmptyChart";
import { useOverviewLoc } from "@/features/overview/overviewWidgets";
import { formatCompactNumber } from "@/lib/utils";
import type { LocPayload, LocPoint, LocProject } from "@/types/stats";

// LOC signature accent + per-project accent. Kept local (not colorAt) so the
// LOC feature keeps a stable, recognizable hue regardless of positional palette
// churn on the rest of the dashboard.
const LOC_ACCENT = "#b967ff"; // electric violet
const LOC_ACCENT_2 = "#05d9e8"; // electric cyan

/** Self-fetching LOC widget. Renders inside whatever card chrome the caller
 * provides (legacy ChartCard or the grid tile). */
export function LinesOfCodeCard() {
  const query = useOverviewLoc();
  const data = query.data;

  if (query.isLoading && !data) {
    return <LocNote text="Measuring lines of code…" />;
  }
  const hasData =
    !!data && (data.totalLoc > 0 || data.overTime.length > 0 || data.perProject.length > 0);
  if (!hasData) {
    return (
      <LocNote
        text="No line-count data in this range yet"
        hint="Lines of code come from your editor's file_lines heartbeats — keep coding and this fills in."
      />
    );
  }
  return <LocContent data={data!} />;
}

function LocContent({ data }: { data: LocPayload }) {
  const projectCount = data.perProject.length;
  return (
    <div className="flex h-full min-h-0 w-full flex-col gap-3">
      {/* Headline */}
      <div className="flex items-end justify-between gap-4">
        <div className="flex items-center gap-3">
          <span
            className="grid h-10 w-10 place-items-center rounded-md"
            style={{
              background: `${LOC_ACCENT}1f`,
              boxShadow: `0 0 18px ${LOC_ACCENT}55`,
              color: LOC_ACCENT,
            }}
            aria-hidden
          >
            <Code2 className="h-5 w-5" />
          </span>
          <div className="flex flex-col">
            <span className="font-mono text-[10px] uppercase tracking-[0.2em] text-[color:var(--muted-foreground)]">
              Lines of code
            </span>
            <span
              className="font-mono text-3xl font-bold leading-none tabular-nums"
              style={{ color: LOC_ACCENT, textShadow: `0 0 22px ${LOC_ACCENT}66` }}
              title={data.totalLoc.toLocaleString()}
            >
              {formatCompactNumber(data.totalLoc)}
            </span>
          </div>
        </div>
        <span className="font-mono text-[11px] uppercase tracking-[0.15em] text-[color:var(--muted-foreground)]">
          across {projectCount} {projectCount === 1 ? "project" : "projects"}
        </span>
      </div>

      {/* Body: over-time area (left) + per-project bars (right). */}
      <div className="grid min-h-0 flex-1 grid-cols-1 gap-4 lg:grid-cols-5">
        <div className="min-h-0 lg:col-span-3">
          <LocOverTimeArea points={data.overTime} />
        </div>
        <div className="min-h-0 lg:col-span-2">
          <LocProjectBars projects={data.perProject} total={data.totalLoc} />
        </div>
      </div>
    </div>
  );
}

/** Filled area of ABSOLUTE total LOC over time (the corpus growth curve). Unlike
 * CumulativeArea this does NOT re-accumulate — each point is already the
 * whole-corpus snapshot from the backend. Y is formatted as compact line counts,
 * not hours. */
function LocOverTimeArea({ points, height = 200 }: { points: LocPoint[]; height?: number }) {
  const data = useMemo(
    () => points.map((p, i) => ({ date: new Date(p.date), loc: p.loc, i })),
    [points],
  );

  const surface = useD3Surface(
    { height, margin: { top: 10, right: 14, bottom: 24, left: 46 } },
    ({ g, innerW, innerH, showTip, hideTip }) => {
      if (data.length === 0) return;
      const fg = cssVar("--muted-foreground");
      const border = cssVar("--border");

      const x = d3
        .scaleTime()
        .domain(d3.extent(data, (d) => d.date) as [Date, Date])
        .range([0, innerW]);
      const yMax = d3.max(data, (d) => d.loc) ?? 0;
      const y = d3.scaleLinear().domain([0, yMax || 1]).nice().range([innerH, 0]);

      gridlines(g, y, { span: innerW, stroke: border });
      styleAxis(
        g.append("g").call(
          d3
            .axisLeft(y)
            .ticks(4)
            .tickFormat((d) => formatCompactNumber(+d)),
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
              .tickValues(thinnedDateTicks(data.map((d) => d.date)))
              .tickFormat((d) => formatDay(d as Date)),
          ),
        { fg, border },
        { domain: "line" },
      );

      // Soft vertical gradient under the line (defs are scoped to this surface).
      const gradId = "loc-area-grad";
      const defs = g.append("defs");
      const grad = defs
        .append("linearGradient")
        .attr("id", gradId)
        .attr("x1", "0")
        .attr("y1", "0")
        .attr("x2", "0")
        .attr("y2", "1");
      grad.append("stop").attr("offset", "0%").attr("stop-color", LOC_ACCENT).attr("stop-opacity", 0.35);
      grad.append("stop").attr("offset", "100%").attr("stop-color", LOC_ACCENT).attr("stop-opacity", 0.02);

      const area = d3
        .area<{ date: Date; loc: number }>()
        .x((d) => x(d.date))
        .y0(innerH)
        .y1((d) => y(d.loc))
        .curve(d3.curveMonotoneX);
      const line = d3
        .line<{ date: Date; loc: number }>()
        .x((d) => x(d.date))
        .y((d) => y(d.loc))
        .curve(d3.curveMonotoneX);

      g.append("path").datum(data).attr("d", area).attr("fill", `url(#${gradId})`);
      g.append("path")
        .datum(data)
        .attr("d", line)
        .attr("fill", "none")
        .attr("stroke", LOC_ACCENT)
        .attr("stroke-width", 2);

      g.selectAll("circle.pt")
        .data(data)
        .join("circle")
        .attr("class", "pt")
        .attr("cx", (d) => x(d.date))
        .attr("cy", (d) => y(d.loc))
        .attr("r", 9)
        .attr("fill", "transparent")
        .on("mousemove", (event, d) => {
          showTip(
            event,
            tooltipHtml({
              title: d3.timeFormat("%d %b %Y")(d.date),
              titleSwatch: LOC_ACCENT,
              rows: [{ label: "Lines of code", value: d.loc.toLocaleString() }],
            }),
          );
        })
        .on("mouseleave", hideTip);
    },
    [data],
  );

  if (data.length === 0) {
    return <EmptyChart height={height} title="No trend yet" hint="Not enough history in range" />;
  }
  return <ChartSurface surface={surface} />;
}

/** Horizontal bar list of the top projects by LOC (+ an "Other" roll-up), each
 * bar width proportional to the largest project. */
function LocProjectBars({ projects, total }: { projects: LocProject[]; total: number }) {
  const { rows, otherLoc } = useMemo(() => {
    const sorted = [...projects].sort((a, b) => b.loc - a.loc);
    const TOP = 7;
    const head = sorted.slice(0, TOP);
    const tail = sorted.slice(TOP);
    const other = tail.reduce((s, p) => s + p.loc, 0);
    return { rows: head, otherLoc: other };
  }, [projects]);

  if (rows.length === 0) {
    return <EmptyChart height={200} title="No projects" />;
  }
  const max = Math.max(rows[0]?.loc ?? 1, 1);

  return (
    <ol className="flex h-full min-h-0 flex-col gap-2 overflow-y-auto pr-1">
      {rows.map((p, i) => {
        const pct = (p.loc / max) * 100;
        const share = total > 0 ? (p.loc / total) * 100 : 0;
        return (
          <li key={p.project} className="flex flex-col gap-1">
            <div className="flex items-baseline justify-between gap-2 font-mono text-[11px]">
              <span className="truncate text-[color:var(--foreground)]" title={p.project}>
                {p.project}
              </span>
              <span className="tabular-nums text-[color:var(--muted-foreground)]">
                {formatCompactNumber(p.loc)}
                <span className="ml-1 opacity-60">{share.toFixed(0)}%</span>
              </span>
            </div>
            <div
              className="h-[7px] w-full overflow-hidden rounded-sm"
              style={{ background: "rgba(127,127,127,0.15)" }}
            >
              <div
                className="h-full rounded-sm"
                style={{
                  width: `${pct}%`,
                  background: `linear-gradient(90deg, ${LOC_ACCENT_2}, ${LOC_ACCENT})`,
                  opacity: 0.9 - i * 0.06,
                }}
                aria-hidden
              />
            </div>
          </li>
        );
      })}
      {otherLoc > 0 && (
        <li className="mt-auto flex items-baseline justify-between gap-2 font-mono text-[10px] uppercase tracking-[0.1em] text-[color:var(--muted-foreground)]">
          <span>+ other</span>
          <span className="tabular-nums">{formatCompactNumber(otherLoc)}</span>
        </li>
      )}
    </ol>
  );
}

function LocNote({ text, hint }: { text: string; hint?: string }) {
  return (
    <div className="flex h-full min-h-[120px] w-full flex-col items-center justify-center gap-1 px-6 text-center">
      <Code2 className="mb-1 h-5 w-5 text-[color:var(--muted-foreground)]" aria-hidden />
      <span className="font-mono text-[12px] uppercase tracking-[0.15em] text-[color:var(--muted-foreground)]">
        {text}
      </span>
      {hint && (
        <span className="max-w-md text-[11px] leading-snug text-[color:var(--muted-foreground)] opacity-75">
          {hint}
        </span>
      )}
    </div>
  );
}
