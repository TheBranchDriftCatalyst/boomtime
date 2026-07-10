import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  BookOpen,
  Clock,
  Code,
  FileText,
  GitBranch,
  Link as LinkIcon,
  Tags,
} from "lucide-react";
import { toast } from "sonner";
import { StatCard } from "@/components/StatCard";
import { Spinner } from "@/components/Spinner";
import { ChartCard } from "@/components/charts/ChartCard";
import { ColumnChart } from "@/components/charts/ColumnChart";
import { FileBarChart } from "@/components/charts/FileBarChart";
import { HourBarChart } from "@/components/charts/HourBarChart";
import { PieChart } from "@/components/charts/PieChart";
import { RadarChart } from "@/components/charts/RadarChart";
import { AuthoringVsReading } from "@/viz/council/AuthoringVsReading";
import { BranchActivity } from "@/viz/council/BranchActivity";
import { BreadthVsDepth } from "@/viz/council/BreadthVsDepth";
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
import { CommitListModal } from "@/modals/CommitListModal";
import { SetTagsModal } from "@/modals/SetTagsModal";
import { useTimeRange } from "@/hooks/useTimeRange";
import { api } from "@/lib/api";
import { daysBetween, secondsToHms } from "@/lib/utils";
import { bucketAvg, bucketDates, bucketGroups, bucketSum } from "@/viz/bucket";

export function Projects() {
  const tr = useTimeRange();
  const [selected, setSelected] = useState<string | null>(null);
  const [tag, setTag] = useState<string | null>(null);

  // Modal state.
  const [commitsFor, setCommitsFor] = useState<string | null>(null);
  const [tagsFor, setTagsFor] = useState<string | null>(null);
  const [initialTags, setInitialTags] = useState<string[]>([]);

  const projectsQuery = useQuery({
    queryKey: ["projects", tr.startISO, tr.endISO],
    queryFn: () => api.getUserProjects({ start: tr.startISO, end: tr.endISO }),
  });

  const projects = useMemo(
    () => projectsQuery.data?.projects ?? [],
    [projectsQuery.data],
  );

  // Default to the first project once the list loads.
  useEffect(() => {
    if (!selected && !tag && projects.length > 0) setSelected(projects[0]);
  }, [projects, selected, tag]);

  const statsQuery = useQuery({
    queryKey: ["project-stats", selected, tag, tr.startISO, tr.endISO, tr.timeLimit],
    enabled: Boolean(selected || tag),
    queryFn: () => {
      const params = {
        start: tr.startISO,
        end: tr.endISO,
        timeLimit: tr.timeLimit,
      };
      return tag
        ? api.getTagStats(tag, params)
        : api.getProject(selected as string, params);
    },
  });

  const stats = statsQuery.data;
  const dates = useMemo(
    () =>
      stats ? daysBetween(new Date(stats.startDate), new Date(stats.endDate)) : [],
    [stats],
  );

  // Bucket the daily activity into ~weekly groups on long ranges so the column
  // chart stays bounded (~60 points) instead of freezing on "All time".
  const groups = useMemo(() => bucketGroups(dates.length), [dates.length]);
  const chartDates = useMemo(() => bucketDates(groups, dates), [groups, dates]);
  const chartDailyTotal = useMemo(
    () => bucketSum(groups, stats?.dailyTotal ?? []),
    [groups, stats],
  );

  // Bucketed series for the viz-council Projects charts. Ratio is averaged over
  // each bucket; entities are averaged too (a bucket's mean distinct-files/day,
  // which is honest — summing daily distinct counts would double-count files
  // touched on multiple days).
  const chartWriteRatio = useMemo(
    () => bucketAvg(groups, stats?.dailyWriteRatio ?? []),
    [groups, stats],
  );
  const chartEntities = useMemo(
    () => bucketAvg(groups, stats?.dailyEntities ?? []).map(Math.round),
    [groups, stats],
  );

  const heading = tag ? `#${tag}` : selected ?? "Projects";

  // Exclude the "Other (N more)" aggregate and the literal "Other" catch-all
  // (no-language browsing/meeting heartbeats) from the most-active pick.
  const isOther = (n: string) => n === "Other" || n.startsWith("Other (");
  const mostActiveLang =
    [...(stats?.languages ?? [])]
      .filter((l) => !isOther(l.name))
      .sort((a, b) => b.totalSeconds - a.totalSeconds)[0]?.name ?? "-";

  async function openTags() {
    if (!selected) return;
    try {
      const res = await api.getProjectTags(selected);
      setInitialTags(res.tags);
      setTagsFor(selected);
    } catch {
      toast.error("Failed to fetch tags");
    }
  }

  async function copyBadge() {
    if (!selected) return;
    try {
      const res = await api.getBadgeLink(selected);
      await navigator.clipboard.writeText(res.badgeUrl);
      toast.success("Badge link copied to clipboard");
    } catch {
      toast.error("Failed to generate the badge link");
    }
  }

  return (
    <div>
      <PageToolbar title={heading}>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="outline" size="sm">
              <BookOpen className="h-4 w-4" />
              Projects
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="max-h-72 overflow-y-auto">
            {projects.map((p) => (
              <DropdownMenuItem
                key={p}
                onSelect={() => {
                  setTag(null);
                  setSelected(p);
                }}
              >
                {p}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>

        <TagFilter value={tag} onChange={setTag} />
        <TimeLimitDropdown value={tr.timeLimit} onChange={tr.setTimeLimit} />
        <DateRangePicker
          numDays={tr.numDays}
          onPreset={tr.setDaysFromToday}
          onRange={tr.setRange}
        />

        <Button
          variant="secondary"
          size="icon"
          title="See time spent per commit"
          disabled={!selected}
          onClick={() => selected && setCommitsFor(selected)}
        >
          <GitBranch className="h-4 w-4" />
        </Button>
        <Button
          variant="secondary"
          size="icon"
          title="Add tags to this project"
          disabled={!selected}
          onClick={openTags}
        >
          <Tags className="h-4 w-4" />
        </Button>
        <Button
          variant="secondary"
          size="icon"
          title="Copy shields.io badge to clipboard"
          disabled={!selected}
          onClick={copyBadge}
        >
          <LinkIcon className="h-4 w-4" />
        </Button>
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
              name="Languages"
              value={stats.languagesCount}
              icon={Code}
              accent="info"
            />
            <StatCard
              name="Files touched"
              value={stats.filesCount}
              icon={FileText}
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
                <ColumnChart
                  dates={chartDates}
                  values={chartDailyTotal}
                  seriesName={heading}
                />
              </ChartCard>
            </div>
            <ChartCard title="Language breakdown">
              <PieChart items={stats.languages} />
            </ChartCard>
          </div>

          <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
            <ChartCard title="Activity per weekday">
              <RadarChart weekDay={stats.weekDay} />
            </ChartCard>
            <ChartCard title="Activity per hour of day">
              <HourBarChart hour={stats.hour} />
            </ChartCard>
          </div>

          <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
            <ChartCard title="Authoring vs reading">
              <AuthoringVsReading
                writeSeconds={stats.writeSeconds}
                readSeconds={stats.readSeconds}
                dates={chartDates}
                ratio={chartWriteRatio}
              />
            </ChartCard>
            <ChartCard
              title={
                stats.branchesCount !== undefined
                  ? `Branch activity (${stats.branchesCount})`
                  : "Branch activity"
              }
            >
              <BranchActivity branches={stats.branches} />
            </ChartCard>
          </div>

          <ChartCard title="Breadth vs depth (time vs files/day)">
            <BreadthVsDepth
              dates={chartDates}
              seconds={chartDailyTotal}
              entities={chartEntities}
            />
          </ChartCard>

          <ChartCard title="Most active files">
            <FileBarChart files={stats.files} />
          </ChartCard>
        </div>
      )}

      <CommitListModal
        project={commitsFor}
        onClose={() => setCommitsFor(null)}
      />
      <SetTagsModal
        project={tagsFor}
        initialTags={initialTags}
        onClose={() => setTagsFor(null)}
      />
    </div>
  );
}
