import type { Ref } from "react";
import { useQuery } from "@tanstack/react-query";
import { Clock, Code, FileText, GitBranch, Link as LinkIcon } from "lucide-react";
import { toast } from "sonner";
import { StatCard } from "@/components/StatCard";
import { QueryGate } from "@/components/QueryGate";
import { ChartCard } from "@/components/ChartCard";
import { EmbedLinkButton } from "@/features/widgets/EmbedActions";
import { ColumnChart } from "@/viz/charts/ColumnChart";
import { FileBarChart } from "@/viz/charts/FileBarChart";
import { HourBarChart } from "@/viz/charts/HourBarChart";
import { PieChart } from "@/viz/charts/PieChart";
import { RadarChart } from "@/viz/charts/RadarChart";
import { AuthoringVsReading } from "@/viz/charts/AuthoringVsReading";
import { BranchActivity } from "@/viz/charts/BranchActivity";
import { BreadthVsDepth } from "@/viz/charts/BreadthVsDepth";
import { Button } from "@/components/ui/button";
import { Combobox } from "@/components/ui/combobox";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { secondsToHms } from "@/lib/utils";
import { mostActive as topByName } from "@/lib/mostActive";
import { useProjectCharts } from "./useProjectCharts";

interface ProjectDetailProps {
  /** The selected project, or null before the list loads. */
  project: string | null;
  /** All known project names (for the selector). */
  projects: string[];
  onSelect: (project: string) => void;
  /** Opens the per-commit time modal for the selected project. */
  onShowCommits: (project: string) => void;
  startISO: string;
  endISO: string;
  timeLimit: number;
  /** Scroll anchor so the page can scroll the detail into view on select. */
  ref?: Ref<HTMLDivElement>;
}

/**
 * Per-project detail: header/selector/actions plus the chart grid for one
 * explicitly selected project.
 */
export function ProjectDetail({
  project,
  projects,
  onSelect,
  onShowCommits,
  startISO,
  endISO,
  timeLimit,
  ref,
}: ProjectDetailProps) {
  const statsQuery = useQuery({
    queryKey: qk.projectStats(project, startISO, endISO, timeLimit),
    enabled: Boolean(project),
    queryFn: () =>
      api.getProject(project as string, {
        start: startISO,
        end: endISO,
        timeLimit,
      }),
  });

  const stats = statsQuery.data;
  const {
    chartDates,
    chartDailyTotal,
    languageColumnSeries,
    chartWriteRatio,
    chartEntities,
  } = useProjectCharts(stats);

  const detailHeading = project ?? "-";
  const mostActiveLang = topByName(stats?.languages ?? []);
  const projectOptions = projects.map((p) => ({ value: p }));

  async function copyBadge() {
    if (!project) return;
    try {
      const res = await api.getBadgeLink(project);
      await navigator.clipboard.writeText(res.badgeUrl);
      toast.success("Badge link copied to clipboard");
    } catch {
      toast.error("Failed to generate the badge link");
    }
  }

  return (
    <section ref={ref} className="scroll-mt-4">
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
            value={project}
            onSelect={onSelect}
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
            disabled={!project}
            onClick={() => project && onShowCommits(project)}
          >
            <GitBranch className="h-4 w-4" />
          </Button>
          <Button
            variant="secondary"
            size="icon"
            title="Copy shields.io badge to clipboard"
            disabled={!project}
            onClick={copyBadge}
          >
            <LinkIcon className="h-4 w-4" />
          </Button>
        </div>
      </div>

      <QueryGate
        query={statsQuery}
        errorMessage={`Failed to load project detail for ${detailHeading}.`}
      >
        {(stats) => (
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
              <ChartCard
                title="Total activity"
                embedAction={
                  project ? (
                    <EmbedLinkButton
                      kind="stats-card"
                      scopeType="project"
                      scopeRef={project}
                    />
                  ) : undefined
                }
              >
                {languageColumnSeries.length > 0 ? (
                  <ColumnChart
                    dates={chartDates}
                    series={languageColumnSeries}
                    seriesName={detailHeading}
                  />
                ) : (
                  <ColumnChart
                    dates={chartDates}
                    values={chartDailyTotal}
                    seriesName={detailHeading}
                  />
                )}
              </ChartCard>
            </div>
            <ChartCard
              title="Language breakdown"
              embedAction={
                project ? (
                  <EmbedLinkButton
                    kind="top-langs"
                    scopeType="project"
                    scopeRef={project}
                  />
                ) : undefined
              }
            >
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
      </QueryGate>
    </section>
  );
}
