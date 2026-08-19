import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Page } from "@shared/layout/Page";
import { WidgetsPanel } from "@shared/features/widgets/WidgetsPanel";
import { DateRangePicker } from "@shared/components/toolbar/DateRangePicker";
import { TimeLimitDropdown } from "@shared/components/toolbar/TimeLimitDropdown";
import { CommitListModal } from "@boomtime/features/projects/CommitListModal";
import { useTimeRange } from "@shared/hooks/useTimeRange";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import { AllProjectsRail } from "@boomtime/features/projects/AllProjectsRail";
import { ProjectDetail } from "@boomtime/features/projects/ProjectDetail";

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
    <Page>
      <Page.Header title="Projects">
        {selected && <WidgetsPanel scopeType="project" scopeRef={selected} />}
        <TimeLimitDropdown value={tr.timeLimit} onChange={tr.setTimeLimit} />
        <DateRangePicker
          numDays={tr.numDays}
          onPreset={tr.setDaysFromToday}
          onRange={tr.setRange}
        />
      </Page.Header>
      <Page.Body>
        <Page.Content>
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
            projectsLoading={projectsQuery.isLoading}
          />

          <CommitListModal
            project={commitsFor}
            onClose={() => setCommitsFor(null)}
          />
        </Page.Content>
      </Page.Body>
    </Page>
  );
}
