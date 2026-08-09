// OverviewWidgetRenderer (gaka-7uc, Phase 3) — dispatches an Overview widget
// kind id to its in-page, SELF-FETCHING React renderer for the composable
// Overview dashboard grid (Phase 4 wires the visible editor).
//
// Unlike the profile WidgetRenderer (which threads a single
// PublicDashboardPayload prop into every kind), Overview widgets self-fetch:
// each per-kind sub-component calls exactly the overviewWidgets hook(s) it
// needs, which read the shared OverviewDataContext and run the SAME react-query
// (identical qk.* key) the legacy inline OverviewDashboard runs — so a
// dashboard rendered as widgets shares the same cache and costs no extra
// network. This mirrors the GoalProgress/GoalRing self-fetch pattern.
//
// HOOKS RULE: React hooks can't be called conditionally, so this is a thin
// switch that renders a small PER-KIND sub-component; each sub-component owns
// its hook calls. Do NOT hoist the hooks into the switch.
//
// Every branch's data wiring mirrors OverviewDashboard.tsx 1:1 (same viz
// component, same props/data slice). See that file for the source of truth.
//
// MUST render inside an <OverviewDataProvider> (Phase 4 provides it); the hooks
// throw otherwise.
import { Calculator, Clock, Code, Crown } from "lucide-react";
import { StatCard } from "@thebranchdriftcatalyst/catalyst-ui/components/StatCard";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dropdown-menu";
import { AIAssistanceCard } from "@/features/overview/AIAssistanceCard";
import { WellnessCard } from "@/features/overview/WellnessCard";
import { LinesOfCodeCard } from "@/features/overview/LinesOfCodeCard";
import {
  GithubCommitsCard,
  GithubReposCard,
  GithubLanguagesCard,
} from "@/features/overview/GithubCharts";
import { ColumnChart } from "@/viz/charts/ColumnChart";
import { HeatmapChart } from "@/viz/charts/HeatmapChart";
import { PieChart } from "@/viz/charts/PieChart";
import { TimelineChart } from "@/viz/charts/TimelineChart";
import { CategoryBreakdown } from "@/viz/charts/CategoryBreakdown";
import { ContributionCalendar } from "@/viz/charts/ContributionCalendar";
import { CumulativeArea } from "@/viz/charts/CumulativeArea";
import { StreakBanner } from "@/viz/charts/StreakBanner";
import { CategoryStreamgraph } from "@/viz/charts/CategoryStreamgraph";
import { Punchcard } from "@/viz/charts/Punchcard";
import { HourBarChart } from "@/viz/charts/HourBarChart";
import { DeepWorkSessions } from "@/viz/charts/DeepWorkSessions";
import { MomentumGrid } from "@/viz/charts/MomentumGrid";
import { secondsToHms } from "@/lib/utils";
import { TIMELINE_HOUR_OPTIONS } from "@/lib/config";
import { useOverviewData } from "@/features/overview/OverviewDataContext";
import {
  useOverviewStats,
  useOverviewTimeline,
  useOverviewPunchcard,
  useOverviewSessions,
  useOverviewMomentum,
  useOverviewAIActivity,
  useOverviewHealthActivity,
} from "@/features/overview/overviewWidgets";
import type { ResourceStats, PunchcardPayload, StatsPayload } from "@/types/stats";
// Part B Stage 3 (gaka-174.x): the data-driven alternative to this file's
// switch cases below, for target:"both" kinds only. Gated behind the
// widgetSpecEngine FE flag — see the matching wiring in WidgetRenderer.tsx's
// file doc for why both engines coexist.
import { usePublicConfig } from "@/lib/usePublicConfig";
import { specForKind } from "@/features/widgets/specs";
import {
  SpecRenderer,
  type SpecRenderData,
} from "@/features/widgets/renderers/SpecRenderer";

export interface OverviewWidgetRendererProps {
  kind: string;
  view?: string;
  /** Opaque per-widget config (gaka-lzr). Thin for now: may carry a `topN`
   * for list widgets. Threaded but otherwise ignored safely. */
  config?: Record<string, unknown>;
}

export function OverviewWidgetRenderer({
  kind,
  view,
  config,
}: OverviewWidgetRendererProps) {
  // Part B Stage 3: target:"both" kinds route through the generic
  // SpecRenderer when the flag is on. fe-only kinds (and everything when the
  // flag is off) fall through to the switch below unchanged. OverviewSpecKind
  // is itself hook-free (see its doc) so this early return stays legal.
  const { config: publicConfig } = usePublicConfig();
  if (publicConfig.widget_spec_engine) {
    const spec = specForKind(kind);
    if (spec?.target === "both") {
      return <OverviewSpecKind kind={kind} view={view} />;
    }
  }

  switch (kind) {
    // --- Stat strip -----------------------------------------------------
    case "overview-stats":
      return <OverviewStats />;

    // --- Ambient overlays (self-hide when the range has no data) ---------
    case "ai-assistance":
      return <OverviewAIAssistance />;
    case "wellness":
      return <OverviewWellness />;

    // --- Categorical + streak -------------------------------------------
    case "category-breakdown":
      return <OverviewCategoryBreakdown />;
    case "streak-banner":
      return <OverviewStreakBanner />;

    // --- Time-series / heatmaps -----------------------------------------
    case "activity-heatmap":
      return <OverviewActivityHeatmap />;
    case "overview-total-activity":
      return <OverviewTotalActivity />;
    case "top-projects":
      return <OverviewTopProjects view={view} config={config} />;
    case "cumulative-area":
      return <OverviewCumulativeArea />;
    case "loc":
      return <LinesOfCodeCard />;

    // --- GitHub-only charts (self-fetch from the cached P2 payload) ------
    case "github-commits":
      return <GithubCommitsCard />;
    case "github-repos":
      return <GithubReposCard />;
    case "github-languages":
      return <GithubLanguagesCard />;
    case "category-streamgraph":
      return <OverviewCategoryStreamgraph />;
    case "heatmap-projects":
      return <OverviewHeatmapProjects />;
    case "heatmap-languages":
      return <OverviewHeatmapLanguages />;

    // --- Patterns -------------------------------------------------------
    case "punchcard":
      return <OverviewPunchcard view={view} />;
    case "momentum":
      return <OverviewMomentum />;
    case "deep-work":
      return <OverviewDeepWork />;

    // --- Recent timeline (carries its own Last-N-hours control) ---------
    case "overview-timeline":
      return <OverviewTimeline />;

    default:
      return <Empty note={`No renderer for "${kind}"`} />;
  }
}

// ---------------------------------------------------------------------------
// Part B Stage 3: SpecRenderer self-fetch wiring for Overview's target:"both"
// kinds. OverviewSpecKind itself calls NO hooks — same HOOKS RULE as the
// top-level switch above — it only dispatches to a leaf component that owns
// exactly the hook(s) its kind's spec needs, so a dashboard with (say) only
// a Total Activity widget doesn't newly fire /punchcard or /sessions
// requests it never needed.
// ---------------------------------------------------------------------------

const EMPTY_PUNCHCARD: PunchcardPayload = { cells: [], maxSeconds: 0, totalSeconds: 0 };

// Zero-value SpecRenderData base. Kinds below only ever read the field(s)
// their own spec panels bind to (see SpecRenderer.tsx's per-primitive
// resolvers) — the rest of the shape is required by the type but unused, so
// a placeholder is safe (mirrors spec.go's resolveSeries returning nil for
// an absent Sessions/Momentum payload rather than erroring).
const EMPTY_SPEC_BASE: SpecRenderData = {
  totalSeconds: 0,
  dailyAvg: 0,
  dailyTotal: [],
  startDate: new Date(0).toISOString(),
  projects: [],
  languages: [],
  editors: [],
  platforms: [],
  categories: [],
  punchcard: EMPTY_PUNCHCARD,
};

function specDataFromStats(stats: StatsPayload | undefined): SpecRenderData {
  return {
    ...EMPTY_SPEC_BASE,
    totalSeconds: stats?.totalSeconds ?? 0,
    dailyAvg: stats?.dailyAvg ?? 0,
    dailyTotal: stats?.dailyTotal ?? [],
    startDate: stats?.startDate ?? EMPTY_SPEC_BASE.startDate,
    projects: stats?.projects ?? [],
    languages: stats?.languages ?? [],
    editors: stats?.editors ?? [],
    platforms: stats?.platforms ?? [],
    categories: stats?.categories ?? [],
  };
}

function OverviewSpecKind({ kind, view }: { kind: string; view?: string }) {
  switch (kind) {
    case "punchcard":
      return <OverviewSpecPunchcard view={view} />;
    case "momentum":
      return <OverviewSpecMomentum />;
    case "deep-work":
      return <OverviewSpecDeepWork />;
    // activity-heatmap, top-projects, cumulative-area, heatmap-projects and
    // heatmap-languages all bind solely to the RAW stats payload — one
    // shared leaf component, same as OverviewActivityHeatmap /
    // OverviewTopProjects / etc. below all independently call
    // useOverviewStats() and share its react-query cache.
    case "activity-heatmap":
    case "top-projects":
    case "cumulative-area":
    case "heatmap-projects":
    case "heatmap-languages":
      return <OverviewSpecFromStats kind={kind} view={view} />;
    default:
      return <Empty note={`No spec renderer for "${kind}"`} />;
  }
}

function OverviewSpecFromStats({ kind, view }: { kind: string; view?: string }) {
  const { stats } = useOverviewStats();
  return <SpecRenderer kind={kind} view={view} data={specDataFromStats(stats)} />;
}

function OverviewSpecPunchcard({ view }: { view?: string }) {
  const query = useOverviewPunchcard();
  return (
    <SpecRenderer
      kind="punchcard"
      view={view}
      data={{ ...EMPTY_SPEC_BASE, punchcard: query.data ?? EMPTY_PUNCHCARD }}
    />
  );
}

function OverviewSpecMomentum() {
  const query = useOverviewMomentum();
  return <SpecRenderer kind="momentum" data={{ ...EMPTY_SPEC_BASE, momentum: query.data }} />;
}

function OverviewSpecDeepWork() {
  const query = useOverviewSessions();
  return <SpecRenderer kind="deep-work" data={{ ...EMPTY_SPEC_BASE, sessions: query.data }} />;
}

// ---------------------------------------------------------------------------
// Per-kind sub-components. Each calls exactly the hook(s) it needs.
// ---------------------------------------------------------------------------

// The 4-tile stat strip: total time, projects count, most-active project,
// most-active language. Mirrors OverviewDashboard's StatCard grid 1:1.
function OverviewStats() {
  const { stats, mostActiveProject, mostActiveLang } = useOverviewStats();
  if (!stats) return <Empty note="Loading…" />;
  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <StatCard
        name="Total tracked time"
        value={secondsToHms(stats.totalSeconds)}
        icon={<Clock className="h-6 w-6" />}
        accent="primary"
      />
      <StatCard
        name="Total projects"
        value={stats.projectsCount}
        icon={<Calculator className="h-6 w-6" />}
        accent="info"
      />
      <StatCard
        name="Most active project"
        value={mostActiveProject}
        icon={<Crown className="h-6 w-6" />}
        accent="success"
      />
      <StatCard
        name="Most active language"
        value={mostActiveLang}
        icon={<Code className="h-6 w-6" />}
        accent="warning"
      />
    </div>
  );
}

// AIAssistanceCard self-hides (returns null) when the range holds no AI-tagged
// heartbeats — no viz slot consumed.
function OverviewAIAssistance() {
  const query = useOverviewAIActivity();
  return <AIAssistanceCard data={query.data} />;
}

// WellnessCard self-hides when the companion app hasn't been paired / range has
// no health data.
function OverviewWellness() {
  const query = useOverviewHealthActivity();
  return <WellnessCard data={query.data} />;
}

// Category breakdown from RAW (non-bucketed) categories, matching the legacy.
function OverviewCategoryBreakdown() {
  const { stats } = useOverviewStats();
  return <CategoryBreakdown categories={stats?.categories ?? []} />;
}

// Streak & consistency from RAW daily totals (current streak excludes today).
function OverviewStreakBanner() {
  const { stats } = useOverviewStats();
  return <StreakBanner dailyTotal={stats?.dailyTotal ?? []} />;
}

// GitHub-style contribution calendar from RAW daily data (parallel to `dates`).
function OverviewActivityHeatmap() {
  const { stats, dates } = useOverviewStats();
  return <ContributionCalendar dates={dates} values={stats?.dailyTotal ?? []} />;
}

// "Total activity" stacked ColumnChart by category, falling back to the single
// daily-total series when there are no categories.
function OverviewTotalActivity() {
  const { chartDates, chartDailyTotal, categoryColumnSeries } =
    useOverviewStats();
  if (categoryColumnSeries.length > 0) {
    return <ColumnChart dates={chartDates} series={categoryColumnSeries} />;
  }
  return <ColumnChart dates={chartDates} values={chartDailyTotal} />;
}

// Project breakdown. Legacy renders a pie of stats.projects; the catalog kind
// also offers a bar view, honored here. `config.topN` optionally caps the set.
function OverviewTopProjects({
  view,
  config,
}: {
  view?: string;
  config?: Record<string, unknown>;
}) {
  const { stats } = useOverviewStats();
  const topN = typeof config?.topN === "number" ? config.topN : undefined;
  const items = stats?.projects ?? [];
  const shown = topN ? items.slice(0, topN) : items;
  if (!shown.length) return <Empty note="No data" />;
  if (view === "bar") return <BarList items={shown.slice(0, 8)} />;
  return <PieChart items={shown} />;
}

// Cumulative coding time from bucketed dates + daily total.
function OverviewCumulativeArea() {
  const { chartDates, chartDailyTotal } = useOverviewStats();
  return <CumulativeArea dates={chartDates} values={chartDailyTotal} />;
}

// Category streamgraph from bucketed categories.
function OverviewCategoryStreamgraph() {
  const { chartCategories, chartDates } = useOverviewStats();
  return <CategoryStreamgraph categories={chartCategories} dates={chartDates} />;
}

// Activity-per-project heatmap (bucketed).
function OverviewHeatmapProjects() {
  const { chartProjects, chartDates } = useOverviewStats();
  return <HeatmapChart items={chartProjects} dates={chartDates} />;
}

// Activity-per-language heatmap (bucketed).
function OverviewHeatmapLanguages() {
  const { chartLanguages, chartDates } = useOverviewStats();
  return <HeatmapChart items={chartLanguages} dates={chartDates} />;
}

// Coding punchcard. Legacy renders the 7×24 heatmap; the catalog kind also
// offers an hour-bars view, honored here (collapse day-of-week → 24 hour bins).
function OverviewPunchcard({ view }: { view?: string }) {
  const query = useOverviewPunchcard();
  if (view === "hour-bars") {
    return <HourBarChart hour={sumPunchcardByHour(query.data)} />;
  }
  return <Punchcard data={query.data} />;
}

// Project momentum (weekly per-project heatmap).
function OverviewMomentum() {
  const query = useOverviewMomentum();
  return <MomentumGrid data={query.data} />;
}

// Deep-work sessions (count + median + longest + daily shape).
function OverviewDeepWork() {
  const query = useOverviewSessions();
  return <DeepWorkSessions data={query.data} />;
}

// Recent timeline. Carries its OWN Last-N-hours control, driven by the shared
// OverviewDataContext (timelineHours/setTimelineHours) so it stays in sync with
// the query key the hook uses.
function OverviewTimeline() {
  const { timelineHours, setTimelineHours } = useOverviewData();
  const query = useOverviewTimeline();
  return (
    <div className="flex h-full flex-col">
      <div className="mb-2 flex justify-end">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="outline" size="sm">
              Last {timelineHours} hours
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            {TIMELINE_HOUR_OPTIONS.map((h) => (
              <DropdownMenuItem key={h} onSelect={() => setTimelineHours(h)}>
                Last {h} hours
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
      <div className="min-h-0 flex-1">
        <TimelineChart timeline={query.data} />
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Small local helpers (mirrors of the profile WidgetRenderer's private bits).
// ---------------------------------------------------------------------------

function BarList({ items }: { items: ResourceStats[] }) {
  const max = Math.max(...items.map((i) => i.totalSeconds), 1);
  return (
    <ol className="flex h-full w-full flex-col gap-1 overflow-y-auto px-2 py-1">
      {items.map((it) => {
        const pct = (it.totalSeconds / max) * 100;
        return (
          <li key={it.name} className="flex flex-col gap-0.5">
            <div className="flex justify-between font-mono text-[10px] uppercase tracking-[0.1em] text-[color:var(--muted-foreground)]">
              <span className="truncate">{it.name}</span>
              <span>{secondsToHms(it.totalSeconds)}</span>
            </div>
            <div
              className="h-[6px] rounded-sm"
              style={{ background: "var(--primary)", width: `${pct}%`, opacity: 0.85 }}
              aria-hidden
            />
          </li>
        );
      })}
    </ol>
  );
}

function Empty({ note }: { note: string }) {
  return (
    <div className="flex h-full w-full items-center justify-center font-mono text-[11px] uppercase tracking-[0.15em] text-[color:var(--muted-foreground)]">
      {note}
    </div>
  );
}

// Sum a punchcard's 7×24 grid down to a single hour-of-day 24-bin series,
// collapsing day-of-week. Emits ResourceStats-shaped rows so HourBarChart
// (which expects `{name, totalSeconds}` per hour) renders without a variant.
function sumPunchcardByHour(pc: PunchcardPayload | undefined): ResourceStats[] {
  const totals = new Array<number>(24).fill(0);
  for (const c of pc?.cells ?? []) {
    if (c.hour >= 0 && c.hour < 24) totals[c.hour] += c.seconds;
  }
  const grand = totals.reduce((s, v) => s + v, 0) || 1;
  return totals.map((total, h) => ({
    name: String(h),
    totalSeconds: total,
    totalPct: (total / grand) * 100,
    totalDaily: [],
    pctDaily: [],
  }));
}
