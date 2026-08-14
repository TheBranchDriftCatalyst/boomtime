// JobsTab — Admin > Jobs (gaka-hney). Operator view of the background-job
// subsystem: the recurring self-scheduling kinds (with a one-click "Run now")
// and a live table of the durable job queue (queued/running/done/failed) with
// a per-row Retry on failures.
//
// Live by design — jobs move fast, so both queries poll on a 5s interval. The
// filters (status select + kind substring) drive the jobs query key so each
// filter combination caches independently; trigger/retry invalidate the whole
// ["admin","jobs"] prefix so the table AND the schedules panel refetch at once.
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  Ban,
  BookOpen,
  CalendarClock,
  DownloadCloud,
  Gauge,
  Headphones,
  ListChecks,
  Play,
  RotateCcw,
  Search,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/select";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/sheet";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/table";
import { EmptyState } from "@/components/EmptyState";
import { JobLogStream } from "@/features/logs/JobLogStream";
import { api, ApiError } from "@/lib/api";
import { usePublicConfig } from "@/lib/usePublicConfig";
import { qk } from "@/lib/queryKeys";
import { relativeTime } from "@/lib/sourceStatus";
import { cn } from "@/lib/utils";
import type { AdminJob, AdminJobQueue, AdminJobStatus } from "@/types/api";

// ── formatting helpers ──────────────────────────────────────────────────────

// "every 8h" / "every 30m" / "every 2d" — picks the coarsest exact unit, else
// falls back to raw seconds.
function humanizeInterval(sec: number): string {
  if (!Number.isFinite(sec) || sec <= 0) return "—";
  if (sec % 86400 === 0) return `every ${sec / 86400}d`;
  if (sec % 3600 === 0) return `every ${sec / 3600}h`;
  if (sec % 60 === 0) return `every ${sec / 60}m`;
  return `every ${sec}s`;
}

// Forward-looking relative label, e.g. "in 6h", "in 30m", "now" (for a fire
// time already elapsed — the scheduler just hasn't ticked yet).
function relativeFuture(ts: string): string {
  const diff = new Date(ts).getTime() - Date.now();
  if (!Number.isFinite(diff) || diff <= 0) return "now";
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return `in ${sec}s`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `in ${min}m`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `in ${hr}h`;
  return `in ${Math.floor(hr / 24)}d`;
}

// Wall-clock duration between start and finish. "—" until both are present.
function jobDuration(started: string | null, finished: string | null): string {
  if (!started || !finished) return "—";
  const ms = new Date(finished).getTime() - new Date(started).getTime();
  if (!Number.isFinite(ms) || ms < 0) return "—";
  if (ms < 1000) return `${ms}ms`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(1)}s`;
  const m = Math.floor(s / 60);
  return `${m}m ${Math.round(s % 60)}s`;
}

// Compact millisecond duration for the throughput line: "820ms", "2.4s",
// "1m 3s". "—" for a non-positive/absent value.
function humanizeMs(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "—";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(1)}s`;
  const m = Math.floor(s / 60);
  return `${m}m ${Math.round(s % 60)}s`;
}

// ── status badge ────────────────────────────────────────────────────────────

const STATUS_STYLES: Record<AdminJobStatus, string> = {
  queued: "border-border bg-muted text-muted-foreground",
  running: "border-amber-500/40 bg-amber-500/15 text-amber-400 animate-pulse",
  done: "border-emerald-500/40 bg-emerald-500/15 text-emerald-400",
  failed: "border-destructive/40 bg-destructive/15 text-destructive",
  cancelled: "border-border bg-muted/60 text-muted-foreground line-through",
};

function StatusBadge({ status }: { status: AdminJobStatus }) {
  return (
    <span
      className={cn(
        "inline-block rounded border px-2 py-0.5 text-[11px] font-semibold uppercase tracking-wider",
        STATUS_STYLES[status] ?? STATUS_STYLES.queued,
      )}
    >
      {status}
    </span>
  );
}

const STATUS_FILTERS: { value: string; label: string }[] = [
  { value: "any", label: "Any status" },
  { value: "queued", label: "Queued" },
  { value: "running", label: "Running" },
  { value: "done", label: "Done" },
  { value: "failed", label: "Failed" },
  { value: "cancelled", label: "Cancelled" },
];

// ── queue overview ──────────────────────────────────────────────────────────

// A single per-kind queue card: the headroom bar (running / maxConcurrency) is
// the centrepiece — filled + labeled "1/1", amber at cap so an operator SEES the
// limiter holding the line. At cap WITH a backlog is "pacing" (durable
// back-pressure: the excess stays status=queued in Postgres and drains as slots
// free). Below it: queue depth, trailing-hour throughput, fail ratio (warn when
// >0), and the kind's last activity.
function QueueCard({ q }: { q: AdminJobQueue }) {
  const unlimited = q.maxConcurrency <= 0;
  const atCap = !unlimited && q.running >= q.maxConcurrency;
  const backPressure = atCap && q.queued > 0;
  const totalRecent = q.doneLastHour + q.failedLastHour;
  const failRatio = totalRecent > 0 ? q.failedLastHour / totalRecent : 0;
  const hasFails = q.failedLastHour > 0;

  // Filled fraction of the headroom bar. Unlimited kinds have no ceiling, so a
  // running kind just shows a full primary bar (no back-pressure is possible).
  const fillPct = unlimited
    ? q.running > 0
      ? 100
      : 0
    : q.maxConcurrency > 0
      ? Math.min(100, (q.running / q.maxConcurrency) * 100)
      : 0;

  const lastStatus = q.lastStatus as AdminJobStatus;
  const knownStatus = lastStatus in STATUS_STYLES;

  return (
    <div
      data-testid={`queue-card-${q.kind}`}
      className={cn(
        "rounded-lg border bg-card p-3 transition-colors",
        atCap ? "border-amber-500/40" : "border-border",
      )}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="truncate font-mono text-sm font-medium text-foreground">
          {q.kind}
        </span>
        {knownStatus && q.lastRunAt ? (
          <StatusBadge status={lastStatus} />
        ) : (
          <span className="text-[11px] text-muted-foreground/50">idle</span>
        )}
      </div>

      {/* Headroom bar + running/max label. */}
      <div className="mt-2.5 flex items-center gap-2">
        <div className="h-2 flex-1 overflow-hidden rounded-full bg-muted">
          <div
            data-testid={`queue-bar-${q.kind}`}
            className={cn(
              "h-full rounded-full transition-all",
              atCap ? "bg-amber-500" : "bg-primary",
            )}
            style={{ width: `${fillPct}%` }}
          />
        </div>
        <span
          className={cn(
            "shrink-0 font-mono text-xs tabular-nums",
            atCap ? "text-amber-400" : "text-muted-foreground",
          )}
          title="running / max concurrency"
        >
          {q.running}/{unlimited ? "∞" : q.maxConcurrency}
        </span>
      </div>

      {/* Back-pressure indicator: at cap WITH a backlog = pacing. */}
      <div className="mt-2 flex flex-wrap items-center gap-1.5 text-[11px]">
        {backPressure ? (
          <span className="inline-flex items-center gap-1 rounded border border-amber-500/40 bg-amber-500/15 px-1.5 py-0.5 font-semibold uppercase tracking-wide text-amber-400">
            <Gauge className="h-3 w-3" />
            pacing
          </span>
        ) : atCap ? (
          <span className="rounded border border-amber-500/40 bg-amber-500/10 px-1.5 py-0.5 font-semibold uppercase tracking-wide text-amber-400">
            at cap
          </span>
        ) : null}
        <span
          className={cn(
            "rounded border px-1.5 py-0.5 tabular-nums",
            q.queued > 0
              ? "border-border bg-muted text-foreground/80"
              : "border-transparent text-muted-foreground/50",
          )}
          title="queued depth"
        >
          {q.queued} queued
        </span>
      </div>

      {/* Throughput + fail ratio + last activity. */}
      <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
        <span className="tabular-nums" title="completed in the last hour">
          {q.doneLastHour}/h done
        </span>
        <span
          data-testid={`queue-fail-${q.kind}`}
          className={cn(
            "inline-flex items-center gap-1 tabular-nums",
            hasFails && "text-destructive",
          )}
          title="failed in the last hour (share of runs)"
        >
          {hasFails && <AlertTriangle className="h-3 w-3" />}
          {q.failedLastHour} failed
          {totalRecent > 0 && ` (${Math.round(failRatio * 100)}%)`}
        </span>
        {q.avgDurationMs > 0 && (
          <span className="tabular-nums" title="mean run duration (last hour)">
            avg {humanizeMs(q.avgDurationMs)}
          </span>
        )}
        <span
          className="tabular-nums"
          title={q.lastRunAt ? new Date(q.lastRunAt).toLocaleString() : undefined}
        >
          last {q.lastRunAt ? relativeTime(q.lastRunAt) : "never"}
        </span>
      </div>
    </div>
  );
}

// The queue overview section: one card per registered/active kind, polled live
// so back-pressure is visible as it happens. Server sorts most-active first
// (running, then queued, then throughput).
function QueueOverview() {
  const { data: queues, isLoading, isError } = useQuery({
    queryKey: qk.adminJobQueues(),
    queryFn: () => api.getJobQueues(),
    refetchInterval: 5000,
  });

  return (
    <section className="space-y-3">
      <h2 className="flex items-center gap-2 font-mono text-xs font-semibold uppercase tracking-widest text-muted-foreground">
        <Gauge className="h-4 w-4 text-primary" />
        Queue
      </h2>
      {isError ? (
        <p className="text-sm text-muted-foreground">
          Queue stats are unavailable (the jobs subsystem may be disabled).
        </p>
      ) : isLoading || !queues ? (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3" aria-busy="true">
          {[0, 1, 2].map((i) => (
            <div key={i} className="h-28 animate-pulse rounded-lg bg-muted/50" />
          ))}
        </div>
      ) : queues.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No job kinds are registered.
        </p>
      ) : (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {queues.map((q) => (
            <QueueCard key={q.kind} q={q} />
          ))}
        </div>
      )}
    </section>
  );
}

// ── reading-steps panel ─────────────────────────────────────────────────────

// On-demand triggers for the catalyst-books pipeline kinds, scoped to the
// current (admin) user. Each enqueues a worker job and returns a jobId; we
// surface it via toast. Gated on books_enabled — the whole panel is inert per
// deployment, mirroring the settings cards. Invalidates the jobs prefix so the
// table below reflects the freshly-queued run.
function ReadingStepsPanel() {
  const qc = useQueryClient();
  const { config } = usePublicConfig();

  const onStepSuccess = (label: string) => (res: { jobId: number }) => {
    toast.success(`${label} started (job #${res.jobId})`);
    qc.invalidateQueries({ queryKey: qk.adminJobsPrefix() });
  };
  const onStepError = (label: string) => (e: unknown) =>
    toast.error(
      e instanceof ApiError ? `Couldn't run ${label}: ${e.message}` : `Couldn't run ${label}`,
    );

  const audibleBackfill = useMutation({
    mutationFn: () => api.backfillAudible(),
    onSuccess: onStepSuccess("Audible backfill"),
    onError: onStepError("Audible backfill"),
  });
  const kindleBackfill = useMutation({
    mutationFn: () => api.backfillKindle(),
    onSuccess: onStepSuccess("Kindle backfill"),
    onError: onStepError("Kindle backfill"),
  });
  const hardcoverMatch = useMutation({
    mutationFn: () => api.matchHardcover(),
    onSuccess: onStepSuccess("Hardcover match"),
    onError: onStepError("Hardcover match"),
  });
  const hardcoverPull = useMutation({
    mutationFn: () => api.pullHardcover(),
    onSuccess: onStepSuccess("Hardcover pull"),
    onError: onStepError("Hardcover pull"),
  });
  const syncAll = useMutation({
    mutationFn: () => api.syncAllBooks(),
    onSuccess: onStepSuccess("Sync all"),
    onError: onStepError("Sync all"),
  });

  if (!config.books_enabled) return null;

  const triggers = [
    { key: "all", label: "Sync all", icon: Play, m: syncAll },
    { key: "audible", label: "Audible backfill", icon: Headphones, m: audibleBackfill },
    { key: "kindle", label: "Kindle backfill", icon: BookOpen, m: kindleBackfill },
    { key: "match", label: "Hardcover match", icon: Search, m: hardcoverMatch },
    { key: "pull", label: "Hardcover pull", icon: DownloadCloud, m: hardcoverPull },
  ];

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 font-mono text-xs font-semibold uppercase tracking-widest text-muted-foreground">
          <BookOpen className="h-4 w-4 text-primary" />
          Run a reading step
        </CardTitle>
      </CardHeader>
      <CardContent>
        <div className="flex flex-wrap items-center gap-2">
          {triggers.map(({ key, label, icon: Icon, m }) => (
            <Button
              key={key}
              variant="outline"
              size="sm"
              onClick={() => m.mutate()}
              disabled={m.isPending}
              title={`Queue a ${label} run for your account`}
            >
              <Icon className="h-3.5 w-3.5" />
              {m.isPending ? "Starting…" : label}
            </Button>
          ))}
        </div>
        <p className="mt-3 text-xs text-muted-foreground">
          On-demand pipeline steps for your own account. Each queues a background job — watch it
          land in the table below.
        </p>
      </CardContent>
    </Card>
  );
}

// ── schedules panel ─────────────────────────────────────────────────────────

function SchedulesPanel() {
  const qc = useQueryClient();
  const { data: schedules, isLoading, isError } = useQuery({
    queryKey: qk.adminJobSchedules(),
    queryFn: () => api.getAdminJobSchedules(),
    refetchInterval: 5000,
  });

  const trigger = useMutation({
    mutationFn: (kind: string) => api.triggerAdminJob(kind),
    onSuccess: (res, kind) => {
      toast.success(`Queued ${kind} (#${res.id})`);
      qc.invalidateQueries({ queryKey: qk.adminJobsPrefix() });
    },
    onError: (e, kind) =>
      toast.error(
        e instanceof ApiError
          ? `Couldn't run ${kind}: ${e.message}`
          : `Couldn't run ${kind}`,
      ),
  });

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 font-mono text-xs font-semibold uppercase tracking-widest text-muted-foreground">
          <CalendarClock className="h-4 w-4 text-primary" />
          Schedules
        </CardTitle>
      </CardHeader>
      <CardContent className="p-0">
        {isError ? (
          <p className="px-4 pb-4 text-sm text-destructive">
            Failed to load schedules.
          </p>
        ) : isLoading || !schedules ? (
          <div className="space-y-2 px-4 pb-4" aria-busy="true">
            {[0, 1, 2].map((i) => (
              <div
                key={i}
                className="h-10 animate-pulse rounded-md bg-muted/50"
              />
            ))}
          </div>
        ) : schedules.length === 0 ? (
          <p className="px-4 pb-4 text-sm text-muted-foreground">
            No recurring jobs are registered.
          </p>
        ) : (
          <ul className="divide-y divide-border/60">
            {schedules.map((s) => (
              <li
                key={s.kind}
                className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 px-4 py-3"
              >
                <div className="min-w-0">
                  <div className="truncate font-mono text-sm font-medium text-foreground">
                    {s.kind}
                  </div>
                  <div className="mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-muted-foreground">
                    <span className="rounded bg-muted/60 px-1.5 py-0.5 font-medium text-foreground/80">
                      {humanizeInterval(s.intervalSeconds)}
                    </span>
                    <span title={new Date(s.nextRun).toLocaleString()}>
                      next {relativeFuture(s.nextRun)}
                    </span>
                    <span
                      title={
                        s.lastRun
                          ? new Date(s.lastRun).toLocaleString()
                          : undefined
                      }
                    >
                      last {s.lastRun ? relativeTime(s.lastRun) : "never"}
                    </span>
                  </div>
                </div>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => trigger.mutate(s.kind)}
                  disabled={trigger.isPending}
                  title={`Queue a ${s.kind} run right now`}
                >
                  <Play className="h-3.5 w-3.5" />
                  Run now
                </Button>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

// ── jobs table ──────────────────────────────────────────────────────────────

function JobRow({
  job,
  onSelect,
  onRetry,
  retrying,
  onCancel,
  cancelling,
}: {
  job: AdminJob;
  onSelect: () => void;
  onRetry: () => void;
  retrying: boolean;
  onCancel: () => void;
  cancelling: boolean;
}) {
  // A job is cancellable while it is still pending (queued) or in flight
  // (running); terminal rows (done/failed/cancelled) only offer Retry on failure.
  const cancellable = job.status === "running" || job.status === "queued";
  // Clicking anywhere on the row opens the per-job log side panel. The Retry /
  // Cancel buttons stopPropagation so they act without also opening the panel.
  return (
    <TableRow
      onClick={onSelect}
      className="cursor-pointer transition-colors hover:bg-muted/40"
      title={`View logs for job #${job.id}`}>
      <TableCell className="font-mono text-xs text-muted-foreground">
        {job.id}
      </TableCell>
      <TableCell className="font-mono text-sm font-medium">{job.kind}</TableCell>
      <TableCell>
        <StatusBadge status={job.status} />
      </TableCell>
      <TableCell className="tabular-nums text-muted-foreground">
        {job.attempts}
        <span className="text-muted-foreground/50">/{job.maxAttempts}</span>
      </TableCell>
      <TableCell
        className="text-muted-foreground"
        title={new Date(job.createdAt).toLocaleString()}
      >
        {relativeTime(job.createdAt)}
      </TableCell>
      <TableCell className="tabular-nums text-muted-foreground">
        {jobDuration(job.startedAt, job.finishedAt)}
      </TableCell>
      <TableCell className="max-w-[20rem]">
        {job.error ? (
          <span
            className="block truncate text-xs text-destructive"
            title={job.error}
          >
            {job.error}
          </span>
        ) : (
          <span className="text-muted-foreground/40">—</span>
        )}
      </TableCell>
      <TableCell className="text-right">
        {job.status === "failed" && (
          <Button
            variant="outline"
            size="sm"
            onClick={(e) => {
              e.stopPropagation();
              onRetry();
            }}
            disabled={retrying}
            title={`Re-enqueue job #${job.id}`}
          >
            <RotateCcw className={cn("h-3.5 w-3.5", retrying && "animate-spin")} />
            Retry
          </Button>
        )}
        {cancellable && (
          <Button
            variant="outline"
            size="sm"
            onClick={(e) => {
              e.stopPropagation();
              onCancel();
            }}
            disabled={cancelling}
            title={`Cancel job #${job.id}`}
          >
            <Ban className={cn("h-3.5 w-3.5", cancelling && "animate-pulse")} />
            Cancel
          </Button>
        )}
      </TableCell>
    </TableRow>
  );
}

const JOB_COLS = 8;

function JobsPanel({
  status,
  kind,
  activeOnly,
}: {
  status: string;
  kind: string;
  activeOnly: boolean;
}) {
  const qc = useQueryClient();
  const trimmedKind = kind.trim();
  // The row whose log panel is open (null = panel closed).
  const [selected, setSelected] = useState<AdminJob | null>(null);
  const { data: jobs, isLoading, isError } = useQuery({
    queryKey: qk.adminJobs(status, trimmedKind, 100),
    queryFn: () =>
      api.getAdminJobs({
        status: status === "any" ? undefined : status,
        kind: trimmedKind || undefined,
        limit: 100,
      }),
    refetchInterval: 5000,
  });

  const retry = useMutation({
    mutationFn: (id: number) => api.retryAdminJob(id),
    onSuccess: (res) => {
      toast.success(`Re-enqueued job #${res.id}`);
      qc.invalidateQueries({ queryKey: qk.adminJobsPrefix() });
    },
    onError: (e) =>
      toast.error(
        e instanceof ApiError ? `Retry failed: ${e.message}` : "Retry failed",
      ),
  });

  const cancel = useMutation({
    mutationFn: (id: number) => api.cancelJob(id),
    onSuccess: (res, id) => {
      toast.success(
        res.cancelled
          ? res.wasRunning
            ? `Cancelling job #${id}…`
            : `Cancelled job #${id}`
          : `Job #${id} already finished`,
      );
      qc.invalidateQueries({ queryKey: qk.adminJobsPrefix() });
    },
    onError: (e) =>
      toast.error(
        e instanceof ApiError ? `Cancel failed: ${e.message}` : "Cancel failed",
      ),
  });

  // "Active only" narrows the loaded page to in-flight rows (queued|running)
  // client-side, composing with the status select + kind filter.
  const visible =
    activeOnly && jobs
      ? jobs.filter((j) => j.status === "queued" || j.status === "running")
      : jobs;

  return (
    <Card>
      <CardContent className="p-0">
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-16">ID</TableHead>
                <TableHead>Kind</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Attempts</TableHead>
                <TableHead>Created</TableHead>
                <TableHead>Duration</TableHead>
                <TableHead>Error</TableHead>
                <TableHead className="text-right" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {isError ? (
                <TableRow>
                  <TableCell
                    colSpan={JOB_COLS}
                    className="py-8 text-center text-sm text-destructive"
                  >
                    Failed to load jobs.
                  </TableCell>
                </TableRow>
              ) : isLoading || !visible ? (
                <SkeletonRows />
              ) : visible.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={JOB_COLS} className="p-0">
                    <EmptyState
                      icon={ListChecks}
                      title="No jobs match"
                      description={
                        activeOnly || status !== "any" || trimmedKind
                          ? "No jobs match the current filters. Clear them, or trigger a run from the Schedules panel above."
                          : "Nothing has been queued yet. Trigger a run from the Schedules panel above."
                      }
                    />
                  </TableCell>
                </TableRow>
              ) : (
                visible.map((job) => (
                  <JobRow
                    key={job.id}
                    job={job}
                    onSelect={() => setSelected(job)}
                    retrying={retry.isPending && retry.variables === job.id}
                    onRetry={() => retry.mutate(job.id)}
                    cancelling={cancel.isPending && cancel.variables === job.id}
                    onCancel={() => cancel.mutate(job.id)}
                  />
                ))
              )}
            </TableBody>
          </Table>
        </div>
      </CardContent>
      <JobDetailSheet
        job={selected}
        onOpenChange={(open) => !open && setSelected(null)}
      />
    </Card>
  );
}

// ── per-job log side panel ──────────────────────────────────────────────────

// A right-side drawer streaming one job's logs. Its header restates the job's
// identity (kind, #id, status, attempts, duration, error), and the body reuses
// the shared server log stream filtered to this job's id (attrs.job_id). The
// job stays live: a running job's lines appear as the worker emits them.
function JobDetailSheet({
  job,
  onOpenChange,
}: {
  job: AdminJob | null;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Sheet open={!!job} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="flex w-full flex-col gap-0 sm:max-w-2xl"
      >
        {job && (
          <>
            <SheetHeader className="space-y-2 pr-8 text-left">
              <SheetTitle className="flex flex-wrap items-center gap-2 font-mono text-sm">
                <span className="font-semibold">{job.kind}</span>
                <span className="text-xs text-muted-foreground">#{job.id}</span>
                <StatusBadge status={job.status} />
              </SheetTitle>
              <SheetDescription asChild>
                <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
                  <span className="tabular-nums">
                    attempts {job.attempts}/{job.maxAttempts}
                  </span>
                  <span className="tabular-nums">
                    duration {jobDuration(job.startedAt, job.finishedAt)}
                  </span>
                  <span title={new Date(job.createdAt).toLocaleString()}>
                    created {relativeTime(job.createdAt)}
                  </span>
                </div>
              </SheetDescription>
              {job.status === "failed" && job.error && (
                <p className="rounded border border-destructive/40 bg-destructive/10 px-2 py-1 font-mono text-xs text-destructive">
                  {job.error}
                </p>
              )}
            </SheetHeader>
            <div className="mt-4 min-h-0 flex-1">
              <JobLogStream jobId={job.id} status={job.status} />
            </div>
          </>
        )}
      </SheetContent>
    </Sheet>
  );
}

function SkeletonRows() {
  return (
    <>
      {[0, 1, 2, 3, 4].map((i) => (
        <TableRow key={i}>
          {Array.from({ length: JOB_COLS }).map((_, j) => (
            <TableCell key={j}>
              <div className="h-4 w-full max-w-[110px] animate-pulse rounded bg-muted" />
            </TableCell>
          ))}
        </TableRow>
      ))}
    </>
  );
}

// ── tab ─────────────────────────────────────────────────────────────────────

export function JobsTab() {
  const [status, setStatus] = useState("any");
  const [kind, setKind] = useState("");
  const [activeOnly, setActiveOnly] = useState(false);

  return (
    <div className="max-w-6xl space-y-6">
      <QueueOverview />
      <ReadingStepsPanel />
      <SchedulesPanel />

      <section className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="flex items-center gap-2 font-mono text-xs font-semibold uppercase tracking-widest text-muted-foreground">
            <ListChecks className="h-4 w-4 text-primary" />
            Jobs
          </h2>
          <div className="flex items-center gap-2">
            <Button
              variant={activeOnly ? "default" : "outline"}
              size="sm"
              aria-pressed={activeOnly}
              onClick={() => setActiveOnly((v) => !v)}
              title="Show only queued or running jobs"
            >
              Active only
            </Button>
            <Input
              value={kind}
              onChange={(e) => setKind(e.target.value)}
              placeholder="Filter by kind…"
              className="h-9 w-44"
            />
            <Select value={status} onValueChange={setStatus}>
              <SelectTrigger className="h-9 w-40">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {STATUS_FILTERS.map((f) => (
                  <SelectItem key={f.value} value={f.value}>
                    {f.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </div>

        <JobsPanel status={status} kind={kind} activeOnly={activeOnly} />
      </section>
    </div>
  );
}
