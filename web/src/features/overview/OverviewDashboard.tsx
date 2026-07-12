import { useCallback, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Calculator, Clock, Code, Crown } from "lucide-react";
import { StatCard } from "@/components/StatCard";
import { QueryGate } from "@/components/QueryGate";
import { ChartCard } from "@/components/ChartCard";
import { WidgetsPanel } from "@/features/widgets/WidgetsPanel";
import { EmbedLinkButton } from "@/features/widgets/EmbedActions";
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
import { DeepWorkSessions } from "@/viz/charts/DeepWorkSessions";
import { MomentumGrid } from "@/viz/charts/MomentumGrid";
import { PageToolbar } from "@/components/toolbar/PageToolbar";
import { DateRangePicker } from "@/components/toolbar/DateRangePicker";
import { TimeLimitDropdown } from "@/components/toolbar/TimeLimitDropdown";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useTimeRange } from "@/hooks/useTimeRange";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { TIMELINE_HOUR_OPTIONS } from "@/lib/config";
import { orderCategories, paletteByName } from "@/viz/d3/color";
import { removeHours, secondsToHms } from "@/lib/utils";
import { mostActive } from "@/lib/mostActive";
import { useBucketedDaily } from "@/viz/useBucketedDaily";
import type { ResourceStats } from "@/types/api";

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
}: OverviewDashboardProps) {
  const tr = useTimeRange();
  const [timelineHours, setTimelineHours] = useState(12);

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
      // gaka-7m4: forward the collapsed-tail members on the Other segment so
      // the stacked-column tooltip can break down what "Other" contains.
      otherMembers: c.otherMembers,
      otherCount: c.otherCount,
    }));
  }, [chartCategories]);

  // Most-active picks exclude the "Other" catch-all + "Other (N more)" bucket
  // (see @/lib/mostActive).
  const mostActiveProject = mostActive(stats?.projects ?? []);
  const mostActiveLang = mostActive(stats?.languages ?? []);

  return (
    <div>
      <PageToolbar title={title}>
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
      </PageToolbar>

      <QueryGate query={statsQuery} errorMessage="Failed to load overview stats.">
        {(stats) => (
        <div className="space-y-6">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
            <StatCard
              name="Total tracked time"
              value={secondsToHms(stats.totalSeconds)}
              icon={Clock}
              accent="primary"
            />
            <StatCard
              name="Total projects"
              value={stats.projectsCount}
              icon={Calculator}
              accent="info"
            />
            <StatCard
              name="Most active project"
              value={mostActiveProject}
              icon={Crown}
              accent="success"
            />
            <StatCard
              name="Most active language"
              value={mostActiveLang}
              icon={Code}
              accent="warning"
            />
          </div>

          {/* Category breakdown — first-class, near the top: "tracked time" is
              more than coding (browsing/meetings/etc). */}
          <ChartCard title="Category breakdown">
            <CategoryBreakdown categories={stats.categories ?? []} />
          </ChartCard>

          {/* Streak & consistency (raw daily; current streak excludes today). */}
          <StreakBanner dailyTotal={stats.dailyTotal} />

          {/* Flagship: GitHub-style contribution calendar from RAW daily data. */}
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
            <ContributionCalendar dates={dates} values={stats.dailyTotal} />
          </ChartCard>

          <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
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
              <PieChart items={stats.projects} />
            </ChartCard>
          </div>

          <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
            <ChartCard title="Cumulative coding time">
              <CumulativeArea dates={chartDates} values={chartDailyTotal} />
            </ChartCard>
            <ChartCard title="Category streamgraph">
              <CategoryStreamgraph
                categories={chartCategories}
                dates={chartDates}
              />
            </ChartCard>
          </div>

          <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
            <ChartCard title="Activity per project">
              <HeatmapChart items={chartProjects} dates={chartDates} />
            </ChartCard>
            <ChartCard
              title="Activity per language"
              embedAction={
                <EmbedLinkButton
                  kind="top-langs"
                  scopeType={space ? "space" : "user"}
                  scopeRef={space ?? ""}
                />
              }
            >
              <HeatmapChart items={chartLanguages} dates={chartDates} />
            </ChartCard>
          </div>

          {/* Patterns: cross-project rhythm & momentum. */}
          <div className="pt-2">
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

          <ChartCard title="Deep-work sessions">
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
    </div>
  );
}
