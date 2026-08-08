// GithubCharts.tsx (gaka-v1k Phase 4) — three GitHub-ONLY chart widgets built
// entirely from the ALREADY-CACHED P2 GithubStatsPayload (qk.githubStats):
//
//   • GithubCommitsCard    — commits over time (contribution grid → weekly area)
//   • GithubReposCard      — top repositories by stars (bar list, linked)
//   • GithubLanguagesCard  — language byte breakdown (stacked bar + legend)
//
// No new backend: these read the same cache entry GithubStatTiles (P3) already
// warms, so mounting all three costs ZERO extra network (react-query dedupes on
// the shared key — see useGithubStatsWidget).
//
// ADDITIVE INVARIANT (bd memories github-stats): every GH surface must degrade
// silently. Each widget card resolves its own three states so it is safe to
// drop ANY ONE of them onto the composable grid in isolation:
//   1. feature off (github_connect_enabled === false)  → render NOTHING
//   2. feature on, not linked / empty payload           → Connect-GitHub CTA
//   3. feature on + data                                → the chart
// The count-based data (commits, stars, bytes) is NOT seconds, so these use
// bespoke count-formatted marks rather than the seconds-coupled ColumnChart /
// PieChart. The area mirrors LinesOfCodeCard's plain (non-accumulating) area —
// CumulativeArea would re-accumulate and label the axis in hours.
//
// GH accent (#39d353) throughout, matching the P3 tiles so all GitHub data on
// the Overview reads as one branded series.

import { useMemo } from "react";
import * as d3 from "d3";
import { GitCommitHorizontal, Github, Star } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Card, CardContent } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { Link } from "react-router";
import { ChartCard } from "@/components/ChartCard";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";
import { cssVar } from "@/viz/d3/useChartFrame";
import { tooltipHtml } from "@/viz/d3/tooltip";
import { formatDay, gridlines, styleAxis, thinnedDateTicks } from "@/viz/d3/axes";
import { EmptyChart } from "@/viz/d3/EmptyChart";
import { colorAt } from "@/viz/d3/color";
import { formatCompactNumber } from "@/lib/utils";
import {
  GH_ACCENT,
  SETTINGS_PROFILE,
  hasGithubData,
  toWeeklyCommits,
  useGithubStatsWidget,
  type CommitsWeekPoint,
} from "@/features/overview/githubStatsWidget";
import type { GithubStatsPayload, GithubTopRepo } from "@/types/github";

// ===========================================================================
// Public widget cards — each self-fetches + resolves the three invariant states
// so it is safe to mount standalone on the composable grid.
// ===========================================================================

/** Commits over time — the weekly-bucketed contribution area. */
export function GithubCommitsCard() {
  const { enabled, query } = useGithubStatsWidget();
  if (!enabled) return null;
  if (query.isLoading) return null;
  if (query.isError || !hasGithubData(query.data)) return <ConnectGithubCta />;
  return <GithubCommitsChart data={query.data!} />;
}

/** Top repositories by stars — a linked bar list. */
export function GithubReposCard() {
  const { enabled, query } = useGithubStatsWidget();
  if (!enabled) return null;
  if (query.isLoading) return null;
  if (query.isError || !hasGithubData(query.data)) return <ConnectGithubCta />;
  return <GithubReposChart data={query.data!} />;
}

/** Language breakdown — a stacked proportion bar with a legend. */
export function GithubLanguagesCard() {
  const { enabled, query } = useGithubStatsWidget();
  if (!enabled) return null;
  if (query.isLoading) return null;
  if (query.isError || !hasGithubData(query.data)) return <ConnectGithubCta />;
  return <GithubLanguagesChart data={query.data!} />;
}

/**
 * GithubChartsSection — the LEGACY OverviewDashboard grouping. The composable
 * grid mounts the three cards individually (each with its own CTA); the legacy
 * dashboard groups all three under one header. The sibling GithubStatTiles owns
 * the Connect-GitHub CTA there, so this section stays silent (renders nothing)
 * until data exists — it never double-renders the CTA.
 */
export function GithubChartsSection() {
  const { enabled, query } = useGithubStatsWidget();
  if (!enabled) return null;
  if (query.isLoading) return null;
  if (query.isError || !hasGithubData(query.data)) return null;
  const data = query.data!;
  return (
    <section className="space-y-3">
      <div className="flex items-center gap-2">
        <Github className="h-4 w-4" style={{ color: GH_ACCENT }} />
        <h2 className="text-sm font-semibold">
          GitHub charts
          {data.login ? (
            <span className="ml-1 font-normal text-muted-foreground">
              @{data.login}
            </span>
          ) : null}
        </h2>
      </div>
      {/* GH-green glow rail marks the whole block as the GitHub series. */}
      <div
        className="grid grid-cols-1 gap-6 rounded-lg lg:grid-cols-2"
        style={{ boxShadow: `inset 3px 0 0 0 ${GH_ACCENT}` }}
      >
        <div className="lg:col-span-2">
          <ChartCard title="Commits over time">
            <GithubCommitsChart data={data} />
          </ChartCard>
        </div>
        <ChartCard title="Top repositories">
          <GithubReposChart data={data} />
        </ChartCard>
        <ChartCard title="Language breakdown">
          <GithubLanguagesChart data={data} />
        </ChartCard>
      </div>
    </section>
  );
}

// ===========================================================================
// Pure chart bodies — take an already-fetched payload, render bare content (no
// outer Card) so the grid tile / ChartCard supplies the frame. Reused by BOTH
// the standalone widget cards and the legacy section.
// ===========================================================================

// --- 1. Commits over time --------------------------------------------------

function GithubCommitsChart({ data }: { data: GithubStatsPayload }) {
  const weeks = useMemo(
    () => toWeeklyCommits(data.contributionGrid),
    [data.contributionGrid],
  );
  return (
    <div className="flex h-full min-h-0 w-full flex-col gap-3">
      <GhHeadline
        icon={<GitCommitHorizontal className="h-5 w-5" />}
        label="Commits"
        value={data.totals.commits}
        note="trailing year"
      />
      <div className="min-h-0 flex-1">
        <CommitsArea weeks={weeks} />
      </div>
    </div>
  );
}

/** Filled area of weekly contribution counts. Like LinesOfCodeCard's area this
 * does NOT re-accumulate — each point is that week's own contribution sum. Y is
 * formatted as compact counts (not hours). */
function CommitsArea({
  weeks,
  height = 200,
}: {
  weeks: CommitsWeekPoint[];
  height?: number;
}) {
  const data = useMemo(
    () =>
      weeks.map((w) => ({ date: new Date(`${w.date}T00:00:00Z`), count: w.count })),
    [weeks],
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
      const yMax = d3.max(data, (d) => d.count) ?? 0;
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

      const gradId = "gh-commits-grad";
      const defs = g.append("defs");
      const grad = defs
        .append("linearGradient")
        .attr("id", gradId)
        .attr("x1", "0")
        .attr("y1", "0")
        .attr("x2", "0")
        .attr("y2", "1");
      grad.append("stop").attr("offset", "0%").attr("stop-color", GH_ACCENT).attr("stop-opacity", 0.35);
      grad.append("stop").attr("offset", "100%").attr("stop-color", GH_ACCENT).attr("stop-opacity", 0.02);

      const area = d3
        .area<{ date: Date; count: number }>()
        .x((d) => x(d.date))
        .y0(innerH)
        .y1((d) => y(d.count))
        .curve(d3.curveMonotoneX);
      const line = d3
        .line<{ date: Date; count: number }>()
        .x((d) => x(d.date))
        .y((d) => y(d.count))
        .curve(d3.curveMonotoneX);

      g.append("path").datum(data).attr("d", area).attr("fill", `url(#${gradId})`);
      g.append("path")
        .datum(data)
        .attr("d", line)
        .attr("fill", "none")
        .attr("stroke", GH_ACCENT)
        .attr("stroke-width", 2);

      g.selectAll("circle.pt")
        .data(data)
        .join("circle")
        .attr("class", "pt")
        .attr("cx", (d) => x(d.date))
        .attr("cy", (d) => y(d.count))
        .attr("r", 9)
        .attr("fill", "transparent")
        .on("mousemove", (event, d) => {
          showTip(
            event,
            tooltipHtml({
              title: `Week of ${d3.timeFormat("%d %b %Y")(d.date)}`,
              titleSwatch: GH_ACCENT,
              rows: [
                {
                  label: "Contributions",
                  value: d.count.toLocaleString(),
                },
              ],
            }),
          );
        })
        .on("mouseleave", hideTip);
    },
    [data],
  );

  if (data.length === 0) {
    return (
      <EmptyChart
        height={height}
        title="No contribution history yet"
        hint="Keep committing and this trend fills in"
      />
    );
  }
  return <ChartSurface surface={surface} />;
}

// --- 2. Top repositories by stars ------------------------------------------

function GithubReposChart({ data }: { data: GithubStatsPayload }) {
  const repos = useMemo(
    () => [...(data.topRepos ?? [])].sort((a, b) => b.stars - a.stars).slice(0, 8),
    [data.topRepos],
  );
  return (
    <div className="flex h-full min-h-0 w-full flex-col gap-3">
      <GhHeadline
        icon={<Star className="h-5 w-5" />}
        label="Stars"
        value={data.totals.stars}
        note={`${repos.length} top ${repos.length === 1 ? "repo" : "repos"}`}
      />
      <div className="min-h-0 flex-1">
        <ReposBars repos={repos} />
      </div>
    </div>
  );
}

/** Horizontal bar list of the top repos by stars; each bar width proportional
 * to the most-starred repo. Names link out to the repo. */
function ReposBars({ repos }: { repos: GithubTopRepo[] }) {
  if (repos.length === 0) {
    return <EmptyChart height={200} title="No starred repositories yet" />;
  }
  const max = Math.max(repos[0]?.stars ?? 1, 1);
  return (
    <ol className="flex h-full min-h-0 flex-col gap-2 overflow-y-auto pr-1">
      {repos.map((r, i) => {
        const pct = (r.stars / max) * 100;
        return (
          <li key={r.url || r.name} className="flex flex-col gap-1">
            <div className="flex items-baseline justify-between gap-2 font-mono text-[11px]">
              <a
                href={r.url}
                target="_blank"
                rel="noreferrer noopener"
                className="truncate text-[color:var(--foreground)] hover:underline"
                title={r.name}
              >
                {r.name}
              </a>
              <span className="flex items-center gap-1 tabular-nums text-[color:var(--muted-foreground)]">
                {r.language ? (
                  <span className="mr-1 opacity-60">{r.language}</span>
                ) : null}
                <Star className="h-3 w-3" style={{ color: GH_ACCENT }} aria-hidden />
                {formatCompactNumber(r.stars)}
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
                  background: `linear-gradient(90deg, ${GH_ACCENT}88, ${GH_ACCENT})`,
                  opacity: 0.95 - i * 0.05,
                }}
                aria-hidden
              />
            </div>
          </li>
        );
      })}
    </ol>
  );
}

// --- 3. Language breakdown -------------------------------------------------

interface LangRow {
  name: string;
  bytes: number;
  pct: number;
  color: string;
}

function GithubLanguagesChart({ data }: { data: GithubStatsPayload }) {
  const { rows, total } = useMemo(() => {
    const langs = [...(data.languages ?? [])].sort((a, b) => b.bytes - a.bytes);
    const sum = langs.reduce((s, l) => s + l.bytes, 0);
    const TOP = 8;
    const head = langs.slice(0, TOP);
    const tail = langs.slice(TOP);
    const otherBytes = tail.reduce((s, l) => s + l.bytes, 0);
    const built: LangRow[] = head.map((l, i) => ({
      name: l.name,
      bytes: l.bytes,
      pct: sum > 0 ? (l.bytes / sum) * 100 : 0,
      color: colorAt(i),
    }));
    if (otherBytes > 0) {
      built.push({
        name: "Other",
        bytes: otherBytes,
        pct: sum > 0 ? (otherBytes / sum) * 100 : 0,
        color: colorAt(head.length),
      });
    }
    return { rows: built, total: sum };
  }, [data.languages]);

  return (
    <div className="flex h-full min-h-0 w-full flex-col gap-3">
      <GhHeadline
        icon={<Github className="h-5 w-5" />}
        label="Code bytes"
        value={total}
        note={`${rows.length} ${rows.length === 1 ? "language" : "languages"}`}
      />
      {rows.length === 0 ? (
        <EmptyChart height={160} title="No language data yet" />
      ) : (
        <>
          {/* Stacked proportion bar (classic GitHub language bar). */}
          <div
            className="flex h-3 w-full overflow-hidden rounded-full"
            style={{ background: "rgba(127,127,127,0.15)" }}
            aria-hidden
          >
            {rows.map((r) => (
              <div
                key={r.name}
                style={{ width: `${r.pct}%`, background: r.color }}
                title={`${r.name} — ${r.pct.toFixed(1)}%`}
              />
            ))}
          </div>
          {/* Legend: dot + name + share. */}
          <ul className="grid min-h-0 flex-1 grid-cols-2 gap-x-4 gap-y-1 overflow-y-auto pr-1 font-mono text-[11px]">
            {rows.map((r) => (
              <li key={r.name} className="flex items-baseline justify-between gap-2">
                <span className="flex min-w-0 items-center gap-1.5">
                  <span
                    className="h-2 w-2 shrink-0 rounded-full"
                    style={{ background: r.color }}
                    aria-hidden
                  />
                  <span
                    className="truncate text-[color:var(--foreground)]"
                    title={r.name}
                  >
                    {r.name}
                  </span>
                </span>
                <span className="tabular-nums text-[color:var(--muted-foreground)]">
                  {r.pct.toFixed(1)}%
                </span>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

// ===========================================================================
// Shared bits — the headline stat + the Connect-GitHub CTA.
// ===========================================================================

function GhHeadline({
  icon,
  label,
  value,
  note,
}: {
  icon: React.ReactNode;
  label: string;
  value: number;
  note?: string;
}) {
  return (
    <div className="flex items-end justify-between gap-4">
      <div className="flex items-center gap-3">
        <span
          className="grid h-10 w-10 place-items-center rounded-md"
          style={{
            background: `${GH_ACCENT}1f`,
            boxShadow: `0 0 18px ${GH_ACCENT}55`,
            color: GH_ACCENT,
          }}
          aria-hidden
        >
          {icon}
        </span>
        <div className="flex flex-col">
          <span className="font-mono text-[10px] uppercase tracking-[0.2em] text-[color:var(--muted-foreground)]">
            {label}
          </span>
          <span
            className="font-mono text-3xl font-bold leading-none tabular-nums"
            style={{ color: GH_ACCENT, textShadow: `0 0 22px ${GH_ACCENT}66` }}
            title={value.toLocaleString()}
          >
            {formatCompactNumber(value)}
          </span>
        </div>
      </div>
      {note ? (
        <span className="font-mono text-[11px] uppercase tracking-[0.15em] text-[color:var(--muted-foreground)]">
          {note}
        </span>
      ) : null}
    </div>
  );
}

/** The "Connect GitHub" empty-state — invariant case (2)'s friendly CTA,
 * matching the P3 GithubStatTiles CTA 1:1. */
function ConnectGithubCta() {
  return (
    <Card className="border-dashed" style={{ borderColor: `${GH_ACCENT}66` }}>
      <CardContent className="flex flex-col items-center gap-3 py-8 text-center">
        <span
          className="flex h-11 w-11 items-center justify-center rounded-full"
          style={{ backgroundColor: `${GH_ACCENT}1f`, color: GH_ACCENT }}
        >
          <Github className="h-5 w-5" />
        </span>
        <div>
          <h3 className="text-sm font-semibold">Connect GitHub</h3>
          <p className="mt-1 max-w-sm text-sm text-muted-foreground">
            Connect GitHub to see your commits, repositories &amp; languages
            alongside your coding time.
          </p>
        </div>
        <Button asChild size="sm">
          <Link to={SETTINGS_PROFILE}>
            <Github className="mr-2 h-4 w-4" />
            Connect GitHub
          </Link>
        </Button>
      </CardContent>
    </Card>
  );
}
