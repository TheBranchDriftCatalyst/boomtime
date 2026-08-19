// Shared mapping + per-job filtering for the server log stream (gaka-f0is).
//
// Both surfaces that render the stream — the full-screen Logs viewer and the
// per-job side panel in Admin > Jobs — go through this module so the line
// shape and the job-scoping rule live in exactly one place.
//
// Contract with the backend (parallel worker, same epic): every job log line
// carries `attrs.job_id` = the job's id as a string, so a job's logs are
// exactly the stream entries where `entry.attrs?.job_id === String(job.id)`.
import type { LogViewerLine } from "@thebranchdriftcatalyst/catalyst-ui/components/LogViewer";
import type { ServerLogEntry } from "@shared/types/api";

// Attrs that are already implied by the per-job panel's context: the panel
// header shows the job's id + kind, and `owner` is the current admin. Hiding
// them from each line cuts noise. Exported so tests assert against the same set.
export const IMPLIED_JOB_ATTRS = ["job_id", "kind", "owner"] as const;

/**
 * Map one captured slog record onto the structural shape `LogViewer` renders.
 * Folds `source` (+ `host`, when present) into the dim attrs tail — the same
 * treatment the main Logs viewer has always given them. `omit` drops attr keys
 * that are redundant in the caller's context (the per-job panel omits
 * job_id/kind/owner — see IMPLIED_JOB_ATTRS).
 */
export function toLogViewerLine(
  entry: ServerLogEntry,
  omit?: readonly string[],
): LogViewerLine {
  const attrs: Record<string, string> = {
    ...(entry.attrs ?? {}),
    source: entry.source,
    ...(entry.host ? { host: entry.host } : {}),
  };
  if (omit) for (const k of omit) delete attrs[k];
  return {
    id: entry.id,
    ts: entry.time,
    level: entry.level,
    message: entry.msg,
    attrs,
  };
}

/** True when a log entry belongs to the given job (backend tags every job log
 *  line with `attrs.job_id` = the job's id as a string). */
export function isJobLog(entry: ServerLogEntry, jobId: number): boolean {
  return entry.attrs?.job_id === String(jobId);
}

/** The subset of a log stream belonging to `jobId`, mapped to LogViewer lines
 *  with the job-implied attrs (job_id/kind/owner) stripped. */
export function jobLogLines(
  logs: ServerLogEntry[],
  jobId: number,
): LogViewerLine[] {
  return logs
    .filter((l) => isJobLog(l, jobId))
    .map((l) => toLogViewerLine(l, IMPLIED_JOB_ATTRS));
}
