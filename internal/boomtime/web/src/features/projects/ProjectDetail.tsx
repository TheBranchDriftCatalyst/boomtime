import type { Ref } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Clock,
  Code,
  FileText,
  FolderGit2,
  GitBranch,
  Link as LinkIcon,
} from "lucide-react";
import { Link } from "react-router";
import { toast } from "sonner";
import { StatCard } from "@thebranchdriftcatalyst/catalyst-ui/components/StatCard";
import { QueryGate } from "@shared/components/QueryGate";
import { ProjectDetailSkeleton } from "@shared/components/Skeletons";
import { EmptyState } from "@shared/components/EmptyState";
import { ChartCard } from "@shared/components/ChartCard";
import { untaggedShareSubtitle } from "@shared/lib/untaggedShare";
import { EmbedLinkButton } from "@shared/features/widgets/EmbedActions";
import { ColumnChart } from "@shared/viz/charts/ColumnChart";
import { FileBarChart } from "@shared/viz/charts/FileBarChart";
import { HourBarChart } from "@shared/viz/charts/HourBarChart";
import { PieChart } from "@shared/viz/charts/PieChart";
import { RadarChart } from "@shared/viz/charts/RadarChart";
import { AuthoringVsReading } from "@shared/viz/charts/AuthoringVsReading";
import { BranchActivity } from "@shared/viz/charts/BranchActivity";
import { BreadthVsDepth } from "@shared/viz/charts/BreadthVsDepth";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Combobox } from "@shared/components/ui/combobox";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import { secondsToHms } from "@shared/lib/utils";
import { mostActive as topByName } from "@shared/lib/mostActive";
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
  /** Whether the parent project list is still loading — distinguishes the
   *  "no project selected yet" skeleton from the genuine "no projects" empty
   *  state (gaka-gbbl.2). */
  projectsLoading?: boolean;
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
  projectsLoading,
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

      {!project ? (
        // No project selected. While the list is still loading (or has entries
        // but auto-select hasn't fired yet), preview the layout; only once the
        // list has resolved empty do we surface the "no projects" CTA — the
        // per-project query is `enabled: Boolean(project)`, so without this the
        // gate below would spin forever.
        projectsLoading || projects.length > 0 ? (
          <ProjectDetailSkeleton />
        ) : (
          <EmptyState
            icon={FolderGit2}
            title="No projects yet"
            description="Once you import your history or connect a plugin, your projects and their per-project charts show up here."
            action={
              <Button asChild size="sm">
                <Link to="/app/import">Import your history</Link>
              </Button>
            }
          />
        )
      ) : (
        <QueryGate
          query={statsQuery}
          errorMessage={`Failed to load project detail for ${detailHeading}.`}
          skeleton={<ProjectDetailSkeleton />}
        >
          {(stats) => (
        <div className="space-y-6">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
            <StatCard
              name={`${detailHeading} · tracked time`}
              value={secondsToHms(stats.totalSeconds)}
              icon={<Clock className="h-6 w-6" />}
              accent="primary"
            />
            <StatCard
              name="Languages"
              value={stats.languagesCount}
              icon={<Code className="h-6 w-6" />}
              accent="info"
            />
            <StatCard
              name="Files touched"
              value={stats.filesCount}
              icon={<FileText className="h-6 w-6" />}
              accent="success"
            />
            <StatCard
              name="Most active language"
              value={mostActiveLang}
              icon={<Code className="h-6 w-6" />}
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
              subtitle={untaggedShareSubtitle(stats.languages, stats.totalSeconds, {
                axis: "language",
              })}
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
      )}
    </section>
  );
}
