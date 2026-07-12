import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { PageToolbar } from "@/components/toolbar/PageToolbar";
import { WidgetsPanel } from "@/features/widgets/WidgetsPanel";
import { DateRangePicker } from "@/components/toolbar/DateRangePicker";
import { TimeLimitDropdown } from "@/components/toolbar/TimeLimitDropdown";
import { CommitListModal } from "@/features/projects/CommitListModal";
import { useTimeRange } from "@/hooks/useTimeRange";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { AllProjectsRail } from "@/features/projects/AllProjectsRail";
import { ProjectDetail } from "@/features/projects/ProjectDetail";

export function Projects() {
  const tr = useTimeRange();
  const [selected, setSelected] = useState<string | null>(null);
  const detailRef = useRef<HTMLDivElement>(null);

  // Modal state.
  const [commitsFor, setCommitsFor] = useState<string | null>(null);

  // Project list (for the selector).
  const projectsQuery = useQuery({
    queryKey: qk.projects(tr.startISO, tr.endISO),
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

  function selectProject(p: string) {
    setSelected(p);
    // Scroll the per-project detail into view.
    requestAnimationFrame(() =>
      detailRef.current?.scrollIntoView({ behavior: "smooth", block: "start" }),
    );
  }

  return (
    <div>
      <PageToolbar title="Projects">
        {selected && <WidgetsPanel scopeType="project" scopeRef={selected} />}
        <TimeLimitDropdown value={tr.timeLimit} onChange={tr.setTimeLimit} />
        <DateRangePicker
          numDays={tr.numDays}
          onPreset={tr.setDaysFromToday}
          onRange={tr.setRange}
        />
      </PageToolbar>

      {/* Aggregate rail across all projects. */}
      <AllProjectsRail
        startISO={tr.startISO}
        endISO={tr.endISO}
        timeLimit={tr.timeLimit}
        onSelectProject={selectProject}
      />

      {/* Per-project detail (explicit selection). */}
      <ProjectDetail
        ref={detailRef}
        project={selected}
        projects={projects}
        onSelect={selectProject}
        onShowCommits={setCommitsFor}
        startISO={tr.startISO}
        endISO={tr.endISO}
        timeLimit={tr.timeLimit}
      />

      <CommitListModal
        project={commitsFor}
        onClose={() => setCommitsFor(null)}
      />
    </div>
  );
}
