// JobLogStream — the job-scoped log panel inside the Admin > Jobs side drawer
// (gaka-f0is, gaka-hney).
//
// Two sources, chosen by the job's lifecycle:
//   - ACTIVE (queued|running): stream live from the server LogHub via
//     `useLogsSocket` (ring-buffer backfill + live entries), filtered to this
//     job's id — the running job's lines appear as the worker emits them.
//   - FINISHED (done|failed|cancelled): the live ring has long since rolled over,
//     so fetch the DURABLE copy persisted to object storage on completion
//     (GET .../logs). A small trash button wipes just that stored object (the
//     job record is kept) and drops the panel back to its empty state.
//
// Both paths render through the shared LogViewer with the same job-line mapping
// (logLine.ts), so stored + live lines look identical.
import { useMemo } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";
import { toast } from "sonner";
import { LogViewer } from "@thebranchdriftcatalyst/catalyst-ui/components/LogViewer";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { useLogsSocket } from "@shared/features/logs/useLogsSocket";
import { jobLogLines } from "@shared/features/logs/logLine";
import { api, ApiError } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import type { AdminJobStatus } from "@shared/types/api";

const PANEL_HEIGHT = "h-[70vh]";

function isFinished(status: AdminJobStatus): boolean {
  return status === "done" || status === "failed" || status === "cancelled";
}

// LiveJobLogs — the running/queued path. Unchanged behavior: subscribe to the
// shared socket and show only this job's lines as they stream in.
function LiveJobLogs({ jobId }: { jobId: number }) {
  const { logs } = useLogsSocket();
  const lines = useMemo(() => jobLogLines(logs, jobId), [logs, jobId]);
  return (
    <LogViewer
      logs={lines}
      height={PANEL_HEIGHT}
      emptyText="No logs yet for this job — it may be queued, or ran before log capture."
    />
  );
}

// StoredJobLogs — the finished path. Fetch the persisted JSONL stream and render
// it; the trash button (top-right) deletes just the stored object behind a
// confirm, then clears the view.
function StoredJobLogs({ jobId }: { jobId: number }) {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: qk.adminJobLogs(jobId),
    queryFn: () => api.getJobLogs(jobId),
    staleTime: 30_000,
  });

  const lines = useMemo(() => jobLogLines(data ?? [], jobId), [data, jobId]);
  const hasLines = lines.length > 0;

  const del = useMutation({
    mutationFn: () => api.deleteJobLogs(jobId),
    onSuccess: () => {
      // Optimistically empty the cached stream so the panel drops to its empty
      // state immediately (no refetch race with the just-deleted object).
      qc.setQueryData(qk.adminJobLogs(jobId), []);
      toast.success("Stored logs deleted");
    },
    onError: (e) =>
      toast.error(
        e instanceof ApiError
          ? `Couldn't delete logs: ${e.message}`
          : "Couldn't delete logs",
      ),
  });

  const onDelete = () => {
    if (
      window.confirm(
        "Delete the stored logs for this job? The job record is kept.",
      )
    ) {
      del.mutate();
    }
  };

  return (
    <div className="relative h-full">
      {hasLines && (
        <div className="absolute right-2 top-2 z-10">
          <Button
            variant="ghost"
            size="icon"
            className="h-8 w-8 text-muted-foreground hover:text-destructive"
            onClick={onDelete}
            disabled={del.isPending}
            aria-label="Delete stored logs"
            title="Delete stored logs (keeps the job record)"
          >
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
      )}
      <LogViewer
        logs={lines}
        height={PANEL_HEIGHT}
        emptyText={
          isLoading
            ? "Loading stored logs…"
            : "No stored logs for this job."
        }
      />
    </div>
  );
}

export function JobLogStream({
  jobId,
  status,
}: {
  jobId: number;
  status: AdminJobStatus;
}) {
  return isFinished(status) ? (
    <StoredJobLogs jobId={jobId} />
  ) : (
    <LiveJobLogs jobId={jobId} />
  );
}
