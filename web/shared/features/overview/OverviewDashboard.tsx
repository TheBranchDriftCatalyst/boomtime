import { useCallback, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Calculator, Clock, Code, Crown } from "lucide-react";
import { StatCard } from "@thebranchdriftcatalyst/catalyst-ui/components/StatCard";
import { QueryGate } from "@shared/components/QueryGate";
import { OverviewSkeleton } from "@shared/components/Skeletons";
import { ChartCard } from "@shared/components/ChartCard";
import { WidgetsPanel } from "@shared/features/widgets/WidgetsPanel";
import { EmbedLinkButton } from "@shared/features/widgets/EmbedActions";
import { AIAssistanceCard } from "@shared/features/overview/AIAssistanceCard";
import { GithubStatTiles } from "@shared/features/overview/GithubStatTiles";
import { GithubChartsSection } from "@shared/features/overview/GithubCharts";
import { WellnessCard } from "@shared/features/overview/WellnessCard";
import { ColumnChart } from "@shared/viz/charts/ColumnChart";
import { HeatmapChart } from "@shared/viz/charts/HeatmapChart";
import { PieChart } from "@shared/viz/charts/PieChart";
import { TimelineChart } from "@shared/viz/charts/TimelineChart";
import { CategoryBreakdown } from "@shared/viz/charts/CategoryBreakdown";
import { ContributionCalendar } from "@shared/viz/charts/ContributionCalendar";
import { CumulativeArea } from "@shared/viz/charts/CumulativeArea";
import { LinesOfCodeCard } from "@shared/features/overview/LinesOfCodeCard";
import { CodingProjectsBreakdown } from "@shared/features/overview/CodingProjectsBreakdown";
import { StreakBanner } from "@shared/viz/charts/StreakBanner";
import { CategoryStreamgraph } from "@shared/viz/charts/CategoryStreamgraph";
import { Punchcard } from "@shared/viz/charts/Punchcard";
import { DeepWorkSessions } from "@shared/viz/charts/DeepWorkSessions";
import { MomentumGrid } from "@shared/viz/charts/MomentumGrid";
import { Page } from "@shared/layout/Page";
import { OverviewDataProvider } from "@shared/features/overview/OverviewDataContext";
import { useFeatureFlag } from "@shared/lib/featureFlags";
import { useDashboardEditor } from "@shared/features/dashboard-edit/DashboardEditor";
import { DateRangePicker } from "@shared/components/toolbar/DateRangePicker";
import { TimeLimitDropdown } from "@shared/components/toolbar/TimeLimitDropdown";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dropdown-menu";
import { useTimeRange } from "@shared/hooks/useTimeRange";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import { TIMELINE_HOUR_OPTIONS } from "@shared/lib/config";
import { orderCategories, paletteByName } from "@shared/viz/d3/color";
import { removeHours, secondsToHms } from "@shared/lib/utils";
import { mostActive } from "@shared/lib/mostActive";
import { useBucketedDaily } from "@shared/viz/useBucketedDaily";
import type { ResourceStats } from "@shared/types/api";

interface OverviewDashboardProps {
  /**
   * When set, every query is scoped to this Space's members (its id is threaded
   * into each query key + `?space=` param). Omitted → the global, unscoped
   * Overview. Callers should also key the element on `space` so switching tabs
   * refetches cleanly.
   */
  space?: string;
  /** Extra controls rendered in the toolbar (e.g. a "Manage" button). */
  toolbarActions?: React.ReactNode;
  /** Toolbar title. */
  title?: string;
  /** Optional content rendered inside the scroll region ABOVE the stats grid
   * (e.g. SpaceView's manage panel). Kept in <Page.Content> so it scrolls with
   * the charts and the toolbar stays pinned. */
  beforeContent?: React.ReactNode;
}

/**
 * The Overview dashboard body, reusable both unscoped (the global Overview) and
 * scoped to a Space. Threads an optional `space` into every query key + api
 * call; no viz components change.
 */
export function OverviewDashboard({
  space,
  toolbarActions,
  title = "Overview",
  beforeContent,
}: OverviewDashboardProps) {
  const tr = useTimeRange();
  const [timelineHours, setTimelineHours] = useState(12);

  // boom-lzr Phase 4: the in-app dashboard editor, STRICTLY behind the
  // default-off `overviewEditor` flag. The hook is called unconditionally (it
  // owns a local store + builds the grid/chrome/sidebar nodes; no network) but
  // its output is only RENDERED when the flag is on — so the flag-off path
  // below is the untouched legacy JSX, byte-identical to what ships today.
  const [editorOn] = useFeatureFlag("overviewEditor");
  // gaka-lzr Phase 6: the editor hook is called unconditionally (see the
  // comment above) but its DB persistence must NOT fire for users who never
  // see the editor — `enabled` gates the GET/PUT to exactly when the flag
  // is on.
  const editor = useDashboardEditor("overview", { enabled: editorOn });

  const statsQuery = useQuery({
    queryKey: qk.stats(tr.startISO, tr.endISO, tr.timeLimit, space),
    queryFn: () =>
      api.getStats({
        start: tr.startISO,
        end: tr.endISO,
        timeLimit: tr.timeLimit,
        space,
      }),
  });

  const timelineQuery = useQuery({
    queryKey: qk.timeline(timelineHours, tr.timeLimit, space),
    queryFn: () =>
      api.getTimeline({
        start: removeHours(new Date(), timelineHours).toISOString(),
        end: new Date().toISOString(),
        timeLimit: tr.timeLimit,
        space,
      }),
  });

  // Council "big-bet" analytics (separate endpoints; bind to the same range).
  const punchcardQuery = useQuery({
    queryKey: qk.punchcard(tr.startISO, tr.endISO, tr.timeLimit, space),
    queryFn: () =>
      api.getPunchcard({
        start: tr.startISO,
        end: tr.endISO,
        timeLimit: tr.timeLimit,
        space,
      }),
  });
  const sessionsQuery = useQuery({
    queryKey: qk.sessions(tr.startISO, tr.endISO, tr.timeLimit, space),
    queryFn: () =>
      api.getSessions({
        start: tr.startISO,
        end: tr.endISO,
        timeLimit: tr.timeLimit,
        space,
      }),
  });
  const momentumQuery = useQuery({
    queryKey: qk.momentum(tr.startISO, tr.endISO, space),
    queryFn: () =>
      api.getMomentum({ start: tr.startISO, end: tr.endISO, top: 8, space }),
  });
  // boom-1l9: AI-assistance metrics — the endpoint returns hasData=false when
  // the user has no AI-tagged heartbeats in the range, so the card just
  // early-returns. Not scoped by Space (AI usage is cross-cutting per user).
  const aiActivityQuery = useQuery({
    queryKey: qk.aiActivity(tr.startISO, tr.endISO),
    queryFn: () =>
      api.getAIActivity({ start: tr.startISO, end: tr.endISO }),
  });
  // Apple Watch / HealthKit metrics — hasData=false short-circuits the card
  // when the user hasn't yet paired the companion app or the range predates
  // ingest, so no wasted viz slot.
  const healthActivityQuery = useQuery({
    queryKey: qk.healthActivity(tr.startISO, tr.endISO),
    queryFn: () =>
      api.getHealthActivity({ start: tr.startISO, end: tr.endISO }),
  });

  const stats = statsQuery.data;

  // Bucket the day-by-day series into ~weekly groups for long ranges so the
  // time charts (column + heatmaps) stay bounded (~60 points) instead of
  // rendering hundreds of daily x-points on all-time.
  const { dates, chartDates, sum } = useBucketedDaily(
    stats?.startDate,
    stats?.endDate,
  );
  const chartDailyTotal = useMemo(
    () => sum(stats?.dailyTotal ?? []),
    [sum, stats?.dailyTotal],
  );
  const bucketItems = useCallback(
    (items: ResourceStats[]) =>
      items.map((it) => ({ ...it, totalDaily: sum(it.totalDaily) })),
    [sum],
  );
  const chartProjects = useMemo(
    () => bucketItems(stats?.projects ?? []),
    [bucketItems, stats?.projects],
  );
  const chartLanguages = useMemo(
    () => bucketItems(stats?.languages ?? []),
    [bucketItems, stats?.languages],
  );
  const chartCategories = useMemo(
    () => bucketItems(stats?.categories ?? []),
    [bucketItems, stats?.categories],
  );

  // Stacked-column series for "Total activity", stacked by category. Uses the
  // SAME `orderCategories` + `paletteByName` contract as the Category
  // streamgraph, so the two charts' order/colors cannot desync. Per-day totals
  // equal the old single-series `chartDailyTotal`, so nothing regresses.
  const categoryColumnSeries = useMemo(() => {
    const ordered = orderCategories(chartCategories);
    const palette = paletteByName(ordered);
    return ordered.map((c) => ({
      name: c.name,
      values: c.totalDaily,
      color: palette.get(c.name)!,
      // boom-7m4: forward the collapsed-tail members on the Other segment so
      // the stacked-column tooltip can break down what "Other" contains.
      otherMembers: c.otherMembers,
      otherCount: c.otherCount,
    }));
  }, [chartCategories]);

  // Most-active picks exclude the "Other" catch-all + "Other (N more)" bucket
  // (see @shared/lib/mostActive).
  const mostActiveProject = mostActive(stats?.projects ?? []);
  const mostActiveLang = mostActive(stats?.languages ?? []);

  // boom-lzr Phase 4: when the editor flag is ON, render the draggable
  // widget-grid path (Edit/Preview toggle in the header, the add-widget rail in
  // the aside during edit, the grid in the content region). The widgets
  // self-fetch through the SAME OverviewDataProvider + qk.* keys the legacy
  // path uses, so react-query dedupes — no extra network. Layout is LOCAL only
  // this phase (seeded from OVERVIEW_DEFAULT_LAYOUT); DB persistence is Phase 6.
  if (editorOn) {
    return (
      <OverviewDataProvider value={{ tr, timelineHours, setTimelineHours, space }}>
        <Page>
          <Page.Header title={title}>
            {toolbarActions}
            <WidgetsPanel
              scopeType={space ? "space" : "user"}
              scopeRef={space ?? ""}
            />
            <TimeLimitDropdown value={tr.timeLimit} onChange={tr.setTimeLimit} />
            <DateRangePicker
              numDays={tr.numDays}
              onPreset={tr.setDaysFromToday}
              onRange={tr.setRange}
            />
            {editor.chrome}
          </Page.Header>
          <Page.Body
            aside={
              editor.isEdit ? <Page.Aside>{editor.sidebar}</Page.Aside> : undefined
            }
          >
            <Page.Content>
              {beforeContent && <div className="mb-6">{beforeContent}</div>}
              {editor.content}
            </Page.Content>
          </Page.Body>
        </Page>
      </OverviewDataProvider>
    );
  }

  return (
    // boom-38v: provide the shared Overview inputs so Phase-3 self-fetching
    // widgets can read them via useOverviewData. The legacy render below is
    // unchanged — the provider adds no DOM, so output stays byte-identical.
    <OverviewDataProvider value={{ tr, timelineHours, setTimelineHours, space }}>
    <Page>
      <Page.Header title={title}>
        {toolbarActions}
        <WidgetsPanel
          scopeType={space ? "space" : "user"}
          scopeRef={space ?? ""}
        />
        <TimeLimitDropdown value={tr.timeLimit} onChange={tr.setTimeLimit} />
        <DateRangePicker
          numDays={tr.numDays}
          onPreset={tr.setDaysFromToday}
          onRange={tr.setRange}
        />
      </Page.Header>
      <Page.Body>
        {/* boom-k0q: opt-in magnetic vertical scroll. `proximity` (see
            .snap-sections) only tugs near a section boundary, so scrolling
            through a chart is never fought; the curated `.snap-section`
            landmarks below give the page a handful of natural rest points. */}
        <Page.Content className="snap-sections">
          {beforeContent && <div className="mb-6">{beforeContent}</div>}

          <QueryGate
            query={statsQuery}
            errorMessage="Failed to load overview stats."
            skeleton={<OverviewSkeleton />}
          >
            {(stats) => (
              <div className="space-y-6">
                <div className="snap-section grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
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

                {/* boom-csx P3: GitHub stat strip — a GH-ONLY surface. Renders
                    nothing when the feature is off, a "Connect GitHub" CTA when
                    unlinked/empty, and GH-branded tiles once connected + synced.
                    Self-fetches via a separate optional query; never blocks the
                    Overview. */}
                <GithubStatTiles />

                {/* boom-1l9: AI-assistance strip — self-hides when the range has no
                    AI-tagged heartbeats (user is on a non-AI plugin, or range is
                    pre-2026-07-03 when wakatime.com started emitting these). */}
                <AIAssistanceCard data={aiActivityQuery.data} />

                {/* Apple Watch / HealthKit overlay — self-hides when the companion
                    app hasn't been paired or the range has no health data. */}
                <WellnessCard data={healthActivityQuery.data} />

                {/* Category breakdown — first-class, near the top: "tracked time" is
                    more than coding (browsing/meetings/etc). */}
                <div className="snap-section">
                  <ChartCard title="Category breakdown">
                    <CategoryBreakdown categories={stats.categories ?? []} />
                  </ChartCard>
                </div>

                {/* Streak & consistency (raw daily; current streak excludes today). */}
                <StreakBanner dailyTotal={stats.dailyTotal} />

                {/* Flagship: GitHub-style contribution calendar from RAW daily data. */}
                <div className="snap-section">
                  <ChartCard
                    title="Contribution calendar"
                    embedAction={
                      <EmbedLinkButton
                        kind="activity-heatmap"
                        scopeType={space ? "space" : "user"}
                        scopeRef={space ?? ""}
                      />
                    }
                  >
                    <ContributionCalendar
                      dates={dates}
                      values={stats.dailyTotal}
                      ghValues={stats.githubDailyTotal}
                    />
                  </ChartCard>
                </div>

                {/* boom-v1k P4: GitHub-only charts — commits-over-time, top
                    repos by stars, and language breakdown, all from the cached
                    P2 payload. Grouped here with the GH tiles + calendar so the
                    GitHub surfaces read together. Self-hides when the feature is
                    off / the user is unlinked (GithubStatTiles above owns the
                    Connect-GitHub CTA), so it never blocks the Overview. Mounted
                    bare (like GithubStatTiles) so a self-hidden section leaves no
                    empty wrapper. */}
                <GithubChartsSection />

                <div className="snap-section grid grid-cols-1 gap-6 lg:grid-cols-3">
                  <div className="lg:col-span-2">
                    <ChartCard
                      title="Total activity"
                      embedAction={
                        <EmbedLinkButton
                          kind="stats-card"
                          scopeType={space ? "space" : "user"}
                          scopeRef={space ?? ""}
                        />
                      }
                    >
                      {categoryColumnSeries.length > 0 ? (
                        <ColumnChart
                          dates={chartDates}
                          series={categoryColumnSeries}
                        />
                      ) : (
                        <ColumnChart dates={chartDates} values={chartDailyTotal} />
                      )}
                    </ChartCard>
                  </div>
                  <ChartCard
                    title="Project breakdown"
                    embedAction={
                      <EmbedLinkButton
                        kind="top-projects"
                        scopeType={space ? "space" : "user"}
                        scopeRef={space ?? ""}
                      />
                    }
                  >
                    {/* boom-canon: the UNSCOPED Overview routes the project
                        breakdown through the query DSL (coding·project·seconds,
                        topN+Other) so it honors canonical PINS and exposes a
                        per-project canonize toggle. The Space-scoped view keeps
                        the legacy space-aware PieChart — the DSL is per-caller
                        (no `space` in a QuerySpec), so swapping it there would
                        silently show the viewer's own projects. */}
                    {space ? (
                      <PieChart items={stats.projects} />
                    ) : (
                      <CodingProjectsBreakdown
                        startISO={tr.startISO}
                        endISO={tr.endISO}
                      />
                    )}
                  </ChartCard>
                </div>

                <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
                  <ChartCard
                    title="Cumulative coding time"
                    embedAction={
                      <EmbedLinkButton
                        kind="cumulative-area"
                        scopeType={space ? "space" : "user"}
                        scopeRef={space ?? ""}
                      />
                    }
                  >
                    <CumulativeArea dates={chartDates} values={chartDailyTotal} />
                  </ChartCard>
                  <ChartCard title="Category streamgraph">
                    <CategoryStreamgraph
                      categories={chartCategories}
                      dates={chartDates}
                    />
                  </ChartCard>
                </div>

                {/* boom-yfg: lines of code — total + per-project + growth over
                    time, derived from file_lines (no GitHub). Self-fetches via
                    the shared OverviewDataProvider, so it degrades to a gentle
                    empty state when the range has no line-count data. */}
                <ChartCard title="Lines of code">
                  <LinesOfCodeCard />
                </ChartCard>

                <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
                  <ChartCard
                    title="Activity per project"
                    embedAction={
                      <EmbedLinkButton
                        kind="heatmap-projects"
                        scopeType={space ? "space" : "user"}
                        scopeRef={space ?? ""}
                      />
                    }
                  >
                    <HeatmapChart items={chartProjects} dates={chartDates} />
                  </ChartCard>
                  <ChartCard
                    title="Activity per language"
                    embedAction={
                      <EmbedLinkButton
                        kind="heatmap-languages"
                        scopeType={space ? "space" : "user"}
                        scopeRef={space ?? ""}
                      />
                    }
                  >
                    <HeatmapChart items={chartLanguages} dates={chartDates} />
                  </ChartCard>
                </div>

                {/* Patterns: cross-project rhythm & momentum. */}
                <div className="snap-section pt-2">
                  <h2 className="mb-1 text-lg font-semibold">Patterns</h2>
                  <p className="text-sm text-muted-foreground">
                    When you code, how deeply you focus, and which projects are heating
                    up.
                  </p>
                </div>

                <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
                  <ChartCard
                    title="Coding punchcard"
                    embedAction={
                      <EmbedLinkButton
                        kind="punchcard"
                        scopeType={space ? "space" : "user"}
                        scopeRef={space ?? ""}
                      />
                    }
                  >
                    <Punchcard data={punchcardQuery.data} />
                  </ChartCard>
                  <ChartCard
                    title="Project momentum (by week)"
                    embedAction={
                      <EmbedLinkButton
                        kind="momentum"
                        scopeType={space ? "space" : "user"}
                        scopeRef={space ?? ""}
                      />
                    }
                  >
                    <MomentumGrid data={momentumQuery.data} />
                  </ChartCard>
                </div>

                <ChartCard
                  title="Deep-work sessions"
                  embedAction={
                    <EmbedLinkButton
                      kind="deep-work"
                      scopeType={space ? "space" : "user"}
                      scopeRef={space ?? ""}
                    />
                  }
                >
                  <DeepWorkSessions data={sessionsQuery.data} />
                </ChartCard>

                <ChartCard
                  title="Recent timeline"
                  action={
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button variant="outline" size="sm">
                          Last {timelineHours} hours
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        {TIMELINE_HOUR_OPTIONS.map((h) => (
                          <DropdownMenuItem
                            key={h}
                            onSelect={() => setTimelineHours(h)}
                          >
                            Last {h} hours
                          </DropdownMenuItem>
                        ))}
                      </DropdownMenuContent>
                    </DropdownMenu>
                  }
                >
                  <TimelineChart timeline={timelineQuery.data} />
                </ChartCard>
              </div>
            )}
          </QueryGate>
        </Page.Content>
      </Page.Body>
    </Page>
    </OverviewDataProvider>
  );
}
