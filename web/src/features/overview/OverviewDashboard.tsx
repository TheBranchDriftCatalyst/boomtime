import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Calculator, Clock, Code, Crown } from "lucide-react";
import { StatCard } from "@/components/StatCard";
import { Spinner } from "@/components/Spinner";
import { ChartCard } from "@/components/charts/ChartCard";
import { ColumnChart } from "@/components/charts/ColumnChart";
import { HeatmapChart } from "@/components/charts/HeatmapChart";
import { PieChart } from "@/components/charts/PieChart";
import { TimelineChart } from "@/components/charts/TimelineChart";
import { CategoryBreakdown } from "@/viz/council/CategoryBreakdown";
import { ContributionCalendar } from "@/viz/council/ContributionCalendar";
import { CumulativeArea } from "@/viz/council/CumulativeArea";
import { StreakBanner } from "@/viz/council/StreakBanner";
import { CategoryStreamgraph } from "@/viz/council/CategoryStreamgraph";
import { Punchcard } from "@/viz/council/Punchcard";
import { DeepWorkSessions } from "@/viz/council/DeepWorkSessions";
import { MomentumGrid } from "@/viz/council/MomentumGrid";
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
import { CHART_COLORS, TIMELINE_HOUR_OPTIONS } from "@/lib/config";
import { daysBetween, removeHours, secondsToHms } from "@/lib/utils";
import { mostActive } from "@/lib/mostActive";
import { bucketDates, bucketGroups, bucketSum } from "@/viz/bucket";
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
    queryKey: ["stats", tr.startISO, tr.endISO, tr.timeLimit, space],
    queryFn: () =>
      api.getStats({
        start: tr.startISO,
        end: tr.endISO,
        timeLimit: tr.timeLimit,
        space,
      }),
  });

  const timelineQuery = useQuery({
    queryKey: ["timeline", timelineHours, tr.timeLimit, space],
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
    queryKey: ["punchcard", tr.startISO, tr.endISO, tr.timeLimit, space],
    queryFn: () =>
      api.getPunchcard({
        start: tr.startISO,
        end: tr.endISO,
        timeLimit: tr.timeLimit,
        space,
      }),
  });
  const sessionsQuery = useQuery({
    queryKey: ["sessions", tr.startISO, tr.endISO, tr.timeLimit, space],
    queryFn: () =>
      api.getSessions({
        start: tr.startISO,
        end: tr.endISO,
        timeLimit: tr.timeLimit,
        space,
      }),
  });
  const momentumQuery = useQuery({
    queryKey: ["momentum", tr.startISO, tr.endISO, space],
    queryFn: () =>
      api.getMomentum({ start: tr.startISO, end: tr.endISO, top: 8, space }),
  });

  const stats = statsQuery.data;
  const dates = useMemo(
    () =>
      stats ? daysBetween(new Date(stats.startDate), new Date(stats.endDate)) : [],
    [stats],
  );

  // Bucket the day-by-day series into ~weekly groups for long ranges so the
  // time charts (column + heatmaps) stay bounded (~60 points) instead of
  // rendering hundreds of daily x-points on all-time.
  const groups = useMemo(() => bucketGroups(dates.length), [dates.length]);
  const chartDates = useMemo(() => bucketDates(groups, dates), [groups, dates]);
  const bucketNums = (arr: number[]) => bucketSum(groups, arr);
  const chartDailyTotal = useMemo(
    () => bucketNums(stats?.dailyTotal ?? []),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [groups, stats],
  );
  const bucketItems = (items: ResourceStats[]) =>
    items.map((it) => ({ ...it, totalDaily: bucketNums(it.totalDaily) }));
  const chartProjects = useMemo(
    () => bucketItems(stats?.projects ?? []),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [groups, stats],
  );
  const chartLanguages = useMemo(
    () => bucketItems(stats?.languages ?? []),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [groups, stats],
  );
  const chartCategories = useMemo(
    () => bucketItems(stats?.categories ?? []),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [groups, stats],
  );

  // Stacked-column series for "Total activity", stacked by category. Uses the
  // SAME ordering (real categories by total desc, then the "Other (…)" bucket)
  // and the SAME palette-by-index as the Category streamgraph / breakdown, so
  // the three charts stay visually consistent. Per-day totals equal the old
  // single-series `chartDailyTotal`, so nothing regresses.
  const categoryColumnSeries = useMemo(() => {
    const isOther = (r: ResourceStats) => r.name.startsWith("Other (");
    const ordered = [
      ...chartCategories
        .filter((c) => !isOther(c))
        .sort((a, b) => b.totalSeconds - a.totalSeconds),
      ...chartCategories.filter(isOther),
    ].filter((c) => c.totalSeconds > 0);
    return ordered.map((c, i) => ({
      name: c.name,
      values: c.totalDaily,
      color: CHART_COLORS[i % CHART_COLORS.length],
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
        <TimeLimitDropdown value={tr.timeLimit} onChange={tr.setTimeLimit} />
        <DateRangePicker
          numDays={tr.numDays}
          onPreset={tr.setDaysFromToday}
          onRange={tr.setRange}
        />
      </PageToolbar>

      {statsQuery.isLoading || !stats ? (
        <Spinner />
      ) : (
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
          <ChartCard title="Contribution calendar">
            <ContributionCalendar dates={dates} values={stats.dailyTotal} />
          </ChartCard>

          <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
            <div className="lg:col-span-2">
              <ChartCard title="Total activity">
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
            <ChartCard title="Project breakdown">
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
            <ChartCard title="Activity per language">
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
            <ChartCard title="Coding punchcard">
              <Punchcard data={punchcardQuery.data} />
            </ChartCard>
            <ChartCard title="Project momentum (by week)">
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
    </div>
  );
}
