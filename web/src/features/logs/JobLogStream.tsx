// JobLogStream — the live, job-scoped log tail rendered inside the Admin >
// Jobs side panel (gaka-f0is).
//
// It subscribes to the SAME server log stream as the full Logs viewer via
// `useLogsSocket` (ring-buffer backfill on connect + live entries, durable
// across reconnects — no new endpoint), then shows only the lines the backend
// tagged with this job's id (attrs.job_id === String(jobId)). Mounted only
// while the panel is open, so the socket connects on open and tears down on
// close. Line rendering, auto-scroll, and the "Jump to latest" affordance all
// come from the shared LogViewer.
import { useMemo } from "react";
import { LogViewer } from "@thebranchdriftcatalyst/catalyst-ui/components/LogViewer";
import { useLogsSocket } from "@/features/logs/useLogsSocket";
import { jobLogLines } from "@/features/logs/logLine";

export function JobLogStream({ jobId }: { jobId: number }) {
  const { logs } = useLogsSocket();
  const lines = useMemo(() => jobLogLines(logs, jobId), [logs, jobId]);

  return (
    <LogViewer
      logs={lines}
      height="h-[70vh]"
      emptyText="No logs yet for this job — it may be queued, or ran before log capture."
    />
  );
}
