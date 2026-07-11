import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { PageToolbar } from "@/components/toolbar/PageToolbar";
import { Spinner } from "@/components/Spinner";
import { CurrentRunPanel } from "@/components/import/CurrentRunPanel";
import { HistoryList } from "@/components/import/HistoryList";
import { RunDetail } from "@/components/import/RunDetail";
import { StartImportForm } from "@/components/import/StartImportForm";
import { useImportJobSocket } from "@/hooks/useImportJobSocket";
import { api } from "@/lib/api";
import { isTerminalState } from "@/types/api";

export function Import() {
  const qc = useQueryClient();

  // The job we're live-bound to (running/queued). Null when nothing is active.
  const [activeJobId, setActiveJobId] = useState<number | null>(null);
  // A historical run the user clicked to inspect read-only.
  const [inspectId, setInspectId] = useState<number | null>(null);
  // Guards the one-time auto-bind so a manual start isn't overridden.
  const [autoBound, setAutoBound] = useState(false);

  const jobsQuery = useQuery({
    queryKey: ["import-jobs"],
    queryFn: () => api.getImportJobs(),
    // Poll the list so history/counters refresh even without a socket.
    refetchInterval: 15_000,
  });

  const jobs = useMemo(() => jobsQuery.data?.jobs ?? [], [jobsQuery.data]);

  // Auto-bind on load: if a queued/running job exists, resume its stream.
  useEffect(() => {
    if (autoBound || jobsQuery.isLoading) return;
    const active = jobs.find(
      (j) => j.state === "queued" || j.state === "running",
    );
    if (active) setActiveJobId(active.id);
    setAutoBound(true);
  }, [autoBound, jobs, jobsQuery.isLoading]);

  const stream = useImportJobSocket(activeJobId);

  // When the active job reaches a terminal state, refresh the history list.
  useEffect(() => {
    if (stream.job && isTerminalState(stream.job.state)) {
      qc.invalidateQueries({ queryKey: ["import-jobs"] });
    }
  }, [stream.job, qc]);

  const cancel = useMutation({
    mutationFn: (id: number) => api.cancelImportJob(id),
    onSuccess: () => {
      toast.success("Cancellation requested");
      qc.invalidateQueries({ queryKey: ["import-jobs"] });
    },
    onError: () => toast.error("Failed to cancel the import job"),
  });

  function onStarted(jobId: number) {
    setInspectId(null);
    setActiveJobId(jobId);
    qc.invalidateQueries({ queryKey: ["import-jobs"] });
  }

  return (
    <div>
      <PageToolbar title="Import" />

      <div className="space-y-6">
        <StartImportForm onStarted={onStarted} />

        {activeJobId != null && stream.job && (
          <CurrentRunPanel
            job={stream.job}
            logs={stream.logs}
            status={stream.status}
            onCancel={() => cancel.mutate(activeJobId)}
            cancelling={cancel.isPending}
          />
        )}

        {activeJobId != null && !stream.job && <Spinner />}

        {inspectId != null && (
          <RunDetail jobId={inspectId} onClose={() => setInspectId(null)} />
        )}

        {jobsQuery.isLoading ? (
          <Spinner />
        ) : (
          <HistoryList
            jobs={jobs}
            selectedId={inspectId}
            onSelect={(id) => {
              // Bind live if the clicked run is still active; else inspect.
              const job = jobs.find((j) => j.id === id);
              if (job && !isTerminalState(job.state)) {
                setInspectId(null);
                setActiveJobId(id);
              } else {
                setInspectId(id);
              }
            }}
          />
        )}
      </div>
    </div>
  );
}
