import { useQuery } from "@tanstack/react-query";
import { Calculator, Clock, Code, Crown } from "lucide-react";
import { StatCard } from "@/components/StatCard";
import { TopProjectsBar } from "@/features/projects/TopProjectsBar";
import { CrossProjectFilesTable } from "@/features/projects/CrossProjectFilesTable";
import { Spinner } from "@/components/Spinner";
import { QueryGate } from "@/components/QueryGate";
import { ChartCard } from "@/components/ChartCard";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { secondsToHms } from "@/lib/utils";
import { mostActive as topByName } from "@/lib/mostActive";

interface AllProjectsRailProps {
  startISO: string;
  endISO: string;
  timeLimit: number;
  /** Called when a project is picked from the Top projects bar. */
  onSelectProject: (project: string) => void;
}

/**
 * The "Across all projects" rail: aggregate stats for the selected range (the
 * same aggregate as the Overview) plus the cross-project active-files table.
 */
export function AllProjectsRail({
  startISO,
  endISO,
  timeLimit,
  onSelectProject,
}: AllProjectsRailProps) {
  // Aggregate stats across ALL projects (same data as Overview).
  const aggQuery = useQuery({
    queryKey: qk.stats(startISO, endISO, timeLimit),
    queryFn: () => api.getStats({ start: startISO, end: endISO, timeLimit }),
  });

  // Top files across ALL projects (lynchpins-first). Bound to the same range +
  // timeLimit as the rail. Not tag-scoped (a cross-project file view).
  const filesQuery = useQuery({
    queryKey: qk.crossProjectFiles(startISO, endISO, timeLimit),
    queryFn: () =>
      api.getCrossProjectFiles({
        start: startISO,
        end: endISO,
        timeLimit,
        limit: 20,
      }),
  });

  return (
    <section className="mb-10">
      <div className="mb-3">
        <h2 className="text-lg font-semibold">Across all projects</h2>
        <p className="text-sm text-muted-foreground">
          Combined totals for the selected range — the same aggregate as your
          Overview.
        </p>
      </div>

      <QueryGate query={aggQuery} errorMessage="Failed to load project stats.">
        {(agg) => (
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
            <TopProjectsBar projects={agg.projects} onSelect={onSelectProject} />
          </ChartCard>

          <ChartCard title="Active files across all projects">
            {filesQuery.isLoading ? (
              <Spinner />
            ) : (
              <CrossProjectFilesTable
                files={filesQuery.data?.files ?? []}
                truncated={filesQuery.data?.truncated}
              />
            )}
          </ChartCard>
        </div>
        )}
      </QueryGate>
    </section>
  );
}
