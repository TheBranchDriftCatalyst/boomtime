import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Calculator,
  Clock,
  Code,
  Crown,
  FileText,
  GitBranch,
  Link as LinkIcon,
  Tags,
} from "lucide-react";
import { toast } from "sonner";
import { StatCard } from "@/components/StatCard";
import { TopProjectsBar } from "@/components/TopProjectsBar";
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
import { Combobox } from "@/components/ui/combobox";
import { CommitListModal } from "@/modals/CommitListModal";
import { SetTagsModal } from "@/modals/SetTagsModal";
import { useTimeRange } from "@/hooks/useTimeRange";
import { api } from "@/lib/api";
import { daysBetween, secondsToHms } from "@/lib/utils";
import { mostActive as topByName } from "@/lib/mostActive";
import { bucketAvg, bucketDates, bucketGroups, bucketSum } from "@/viz/bucket";

export function Projects() {
  const tr = useTimeRange();
  const [selected, setSelected] = useState<string | null>(null);
  const [tag, setTag] = useState<string | null>(null);
  const detailRef = useRef<HTMLDivElement>(null);

  // Modal state.
  const [commitsFor, setCommitsFor] = useState<string | null>(null);
  const [tagsFor, setTagsFor] = useState<string | null>(null);
  const [initialTags, setInitialTags] = useState<string[]>([]);

  // --- TOP RAIL: aggregate stats across ALL projects (same data as Overview) --
  const aggQuery = useQuery({
    queryKey: ["stats", tr.startISO, tr.endISO, tr.timeLimit, tag],
    queryFn: () =>
      api.getStats({
        start: tr.startISO,
        end: tr.endISO,
        timeLimit: tr.timeLimit,
        tag: tag ?? undefined,
      }),
  });
  const agg = aggQuery.data;

  // --- Project list (for the selector) ---------------------------------------
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
    if (!selected && projects.length > 0) setSelected(projects[0]);
  }, [projects, selected]);

  // --- BELOW: per-project (or per-tag) detail --------------------------------
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

  // Bucketed series for the viz-council Projects charts. Ratio + entities are
  // averaged over each bucket (summing daily distinct counts would double-count
  // files touched on multiple days).
  const chartWriteRatio = useMemo(
    () => bucketAvg(groups, stats?.dailyWriteRatio ?? []),
    [groups, stats],
  );
  const chartEntities = useMemo(
    () => bucketAvg(groups, stats?.dailyEntities ?? []).map(Math.round),
    [groups, stats],
  );

  const detailHeading = tag ? `#${tag}` : selected ?? "-";
  const mostActiveLang = topByName(stats?.languages ?? []);

  function selectProject(p: string) {
    setTag(null);
    setSelected(p);
    // Scroll the per-project detail into view.
    requestAnimationFrame(() =>
      detailRef.current?.scrollIntoView({ behavior: "smooth", block: "start" }),
    );
  }

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

  const projectOptions = useMemo(
    () => projects.map((p) => ({ value: p })),
    [projects],
  );

  return (
    <div>
      <PageToolbar title="Projects">
        <TagFilter value={tag} onChange={setTag} />
        <TimeLimitDropdown value={tr.timeLimit} onChange={tr.setTimeLimit} />
        <DateRangePicker
          numDays={tr.numDays}
          onPreset={tr.setDaysFromToday}
          onRange={tr.setRange}
        />
      </PageToolbar>

      {/* ===== TOP RAIL — aggregate across all projects ===== */}
      <section className="mb-10">
        <div className="mb-3">
          <h2 className="text-lg font-semibold">Across all projects</h2>
          <p className="text-sm text-muted-foreground">
            Combined totals for the selected range{tag ? ` · #${tag}` : ""} — the
            same aggregate as your Overview.
          </p>
        </div>

        {aggQuery.isLoading || !agg ? (
          <Spinner />
        ) : (
          <div className="space-y-6">
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
              <StatCard
                name="Total tracked time"
                value={secondsToHms(agg.totalSeconds)}
                icon={Clock}
                accent="primary"
              />
              <StatCard
                name="Total projects"
                value={agg.projectsCount}
                icon={Calculator}
                accent="info"
              />
              <StatCard
                name="Most active project"
                value={topByName(agg.projects)}
                icon={Crown}
                accent="success"
              />
              <StatCard
                name="Most active language"
                value={topByName(agg.languages)}
                icon={Code}
                accent="warning"
              />
            </div>

            <ChartCard title="Top projects">
              <TopProjectsBar projects={agg.projects} onSelect={selectProject} />
            </ChartCard>
          </div>
        )}
      </section>

      {/* ===== BELOW — per-project detail (explicit selection) ===== */}
      <section ref={detailRef} className="scroll-mt-4">
        <div className="mb-3 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
          <div>
            <h2 className="text-lg font-semibold">Project detail</h2>
            <p className="text-sm text-muted-foreground">
              Pick a project to see its charts, files, and branch activity.
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm text-muted-foreground">Project:</span>
            <Combobox
              options={projectOptions}
              value={tag ? `#${tag}` : selected}
              onSelect={selectProject}
              fullWidth={false}
              className="min-w-56"
              placeholder="Select a project..."
              searchPlaceholder="Search projects..."
              emptyText="No projects found."
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
          </div>
        </div>

        {statsQuery.isLoading || !stats ? (
          <Spinner />
        ) : (
          <div className="space-y-6">
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
              <StatCard
                name={`${detailHeading} · tracked time`}
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
                    seriesName={detailHeading}
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
      </section>

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
