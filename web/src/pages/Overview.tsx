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
import { PageToolbar } from "@/components/toolbar/PageToolbar";
import { DateRangePicker } from "@/components/toolbar/DateRangePicker";
import { TagFilter } from "@/components/toolbar/TagFilter";
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
import { TIMELINE_HOUR_OPTIONS } from "@/lib/config";
import { daysBetween, removeHours, secondsToHms } from "@/lib/utils";

export function Overview() {
  const tr = useTimeRange();
  const [tag, setTag] = useState<string | null>(null);
  const [timelineHours, setTimelineHours] = useState(12);

  const statsQuery = useQuery({
    queryKey: ["stats", tr.startISO, tr.endISO, tr.timeLimit, tag],
    queryFn: () =>
      api.getStats({
        start: tr.startISO,
        end: tr.endISO,
        timeLimit: tr.timeLimit,
        tag: tag ?? undefined,
      }),
  });

  const timelineQuery = useQuery({
    queryKey: ["timeline", timelineHours, tr.timeLimit],
    queryFn: () =>
      api.getTimeline({
        start: removeHours(new Date(), timelineHours).toISOString(),
        end: new Date().toISOString(),
        timeLimit: tr.timeLimit,
      }),
  });

  const stats = statsQuery.data;
  const dates = useMemo(
    () =>
      stats ? daysBetween(new Date(stats.startDate), new Date(stats.endDate)) : [],
    [stats],
  );

  // Bucket the day-by-day series into ~weekly groups for long ranges so the
  // time charts (column + heatmaps) stay bounded (~60 points) instead of
  // rendering hundreds of daily x-points, which freezes ApexCharts on all-time.
  const MAX_CHART_POINTS = 62;
  const groups = useMemo<number[][]>(() => {
    const n = dates.length;
    if (n <= MAX_CHART_POINTS) return dates.map((_, i) => [i]);
    const size = Math.ceil(n / MAX_CHART_POINTS);
    const g: number[][] = [];
    for (let i = 0; i < n; i += size) {
      g.push(Array.from({ length: Math.min(size, n - i) }, (_, k) => i + k));
    }
    return g;
  }, [dates]);
  const chartDates = useMemo(() => groups.map((gr) => dates[gr[0]]), [groups, dates]);
  const bucketNums = (arr: number[]) =>
    groups.map((gr) => gr.reduce((s, i) => s + (arr[i] ?? 0), 0));
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

  // Exclude the aggregated "Other (N more)" bucket when picking the single most
  // active resource.
  const isOther = (n: string) => n.startsWith("Other (");
  const mostActiveProject =
    [...(stats?.projects ?? [])]
      .filter((r) => !isOther(r.name))
      .sort((a, b) => b.totalSeconds - a.totalSeconds)[0]?.name ?? "-";
  const mostActiveLang =
    [...(stats?.languages ?? [])]
      .filter((r) => !isOther(r.name))
      .sort((a, b) => b.totalSeconds - a.totalSeconds)[0]?.name ?? "-";

  return (
    <div>
      <PageToolbar title="Overview">
        <TagFilter value={tag} onChange={setTag} />
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
              name="Total coding time"
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

          <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
            <div className="lg:col-span-2">
              <ChartCard title="Total activity">
                <ColumnChart dates={chartDates} values={chartDailyTotal} />
              </ChartCard>
            </div>
            <ChartCard title="Project breakdown">
              <PieChart items={stats.projects} />
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
