// JobsTab — Admin > Jobs (gaka-hney). Operator view of the background-job
// subsystem. The queue overview and the durable job history are FUSED into a
// single grouped-by-kind table: each kind is one collapsible header row carrying
// its live aggregate stats inline (state dot + running/max headroom, queue depth,
// failures, trailing-hour throughput, last activity), and expanding a kind
// reveals its recent runs, paginated in place. Collapsed groups stay one line so
// the whole tab reads in a single viewport — no card sprawl, no page scroll.
//
// Below it sit the (unchanged) on-demand "Run a reading step" triggers and the
// recurring "Schedules" panel.
//
// Live by design — jobs move fast, so the queue + expanded-kind queries poll on a
// 5s interval. Every mutation (trigger/retry/cancel + the log-clears) invalidates
// the shared ["admin","jobs"] prefix so the group headers, the open rows, AND the
// schedules panel refetch at once.
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  Ban,
  BookOpen,
  CalendarClock,
  ChevronDown,
  ChevronRight,
  DownloadCloud,
  Gauge,
  Headphones,
  ListChecks,
  Play,
  RotateCcw,
  Search,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
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

// ── grouped jobs: one kind = one collapsible header carrying its live stats ──

// The runs table inside an expanded kind. Newest-first, and page it here in the
// FE so an active kind can't balloon the tab — mirrors the Books explorer's
// leaf-page pagination (a bounded fetch, sliced Prev/Next).
const PAGE_SIZE = 8;

function RunRow({
  job,
  onSelect,
  onRetry,
  retrying,
  onCancel,
  cancelling,
  onClearLogs,
  clearing,
}: {
  job: AdminJob;
  onSelect: () => void;
  onRetry: () => void;
  retrying: boolean;
  onCancel: () => void;
  cancelling: boolean;
  onClearLogs: () => void;
  clearing: boolean;
}) {
  // Cancellable while pending (queued) or in flight (running); terminal rows
  // (done/failed/cancelled) offer Retry only on failure. Clicking the row opens
  // the log side panel; the action buttons stopPropagation so they act alone.
  const cancellable = job.status === "running" || job.status === "queued";
  return (
    <TableRow
      onClick={onSelect}
      className="cursor-pointer transition-colors hover:bg-muted/40"
      title={`View logs for job #${job.id}`}
    >
      <TableCell className="font-mono text-xs text-muted-foreground">{job.id}</TableCell>
      <TableCell>
        <StatusBadge status={job.status} />
      </TableCell>
      <TableCell className="tabular-nums text-muted-foreground">
        {job.attempts}
        <span className="text-muted-foreground/50">/{job.maxAttempts}</span>
      </TableCell>
      <TableCell
        className="tabular-nums text-muted-foreground"
        title={new Date(job.createdAt).toLocaleString()}
      >
        {relativeTime(job.createdAt)}
      </TableCell>
      <TableCell className="tabular-nums text-muted-foreground">
        {jobDuration(job.startedAt, job.finishedAt)}
      </TableCell>
      <TableCell className="text-right">
        <div className="flex items-center justify-end gap-1">
          {job.error && (
            <span
              className="mr-1 hidden max-w-[14rem] truncate text-xs text-destructive md:inline"
              title={job.error}
            >
              {job.error}
            </span>
          )}
          {job.status === "failed" && (
            <Button
              variant="ghost"
              size="sm"
              className="h-7 px-2"
              onClick={(e) => {
                e.stopPropagation();
                onRetry();
              }}
              disabled={retrying}
              title={`Re-enqueue job #${job.id}`}
            >
              <RotateCcw className={cn("h-3.5 w-3.5", retrying && "animate-spin")} />
            </Button>
          )}
          {cancellable && (
            <Button
              variant="ghost"
              size="sm"
              className="h-7 px-2"
              onClick={(e) => {
                e.stopPropagation();
                onCancel();
              }}
              disabled={cancelling}
              title={`Cancel job #${job.id}`}
            >
              <Ban className={cn("h-3.5 w-3.5", cancelling && "animate-pulse")} />
            </Button>
          )}
          <Button
            variant="ghost"
            size="sm"
            className="h-7 px-2 text-muted-foreground hover:text-destructive"
            onClick={(e) => {
              e.stopPropagation();
              onClearLogs();
            }}
            disabled={clearing}
            title={`Clear stored logs for job #${job.id}`}
            aria-label={`clear logs for job ${job.id}`}
          >
            <Trash2 className={cn("h-3.5 w-3.5", clearing && "animate-pulse")} />
          </Button>
        </div>
      </TableCell>
    </TableRow>
  );
}

// A single kind's stat chips + headroom bar (the header's right side), read
// straight off the queue-overview row — no recompute.
function KindStats({ q }: { q: AdminJobQueue }) {
  const unlimited = q.maxConcurrency <= 0;
  const atCap = !unlimited && q.running >= q.maxConcurrency;
  const backPressure = atCap && q.queued > 0;
  const hasFails = q.failedLastHour > 0;
  const fillPct = unlimited
    ? q.running > 0
      ? 100
      : 0
    : Math.min(100, (q.running / q.maxConcurrency) * 100);

  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
      {/* running / max headroom */}
      <span className="flex items-center gap-1.5" title="running / max concurrency">
        <span
          data-testid={`job-group-bar-${q.kind}`}
          className="h-1.5 w-14 overflow-hidden rounded-full bg-muted"
        >
          <span
            className={cn(
              "block h-full rounded-full transition-all",
              atCap ? "bg-amber-500" : "bg-primary",
            )}
            style={{ width: `${fillPct}%` }}
          />
        </span>
        <span
          className={cn("font-mono tabular-nums", atCap ? "text-amber-400" : "text-foreground/80")}
        >
          {q.running}/{unlimited ? "∞" : q.maxConcurrency}
        </span>
        <span className="text-muted-foreground/70">running</span>
      </span>

      {/* back-pressure flag: at cap + backlog = pacing */}
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

      {/* queue depth */}
      <span
        className={cn("tabular-nums", q.queued > 0 ? "text-foreground/80" : "text-muted-foreground/50")}
        title="queued depth"
      >
        {q.queued} queued
      </span>

      {/* trailing-hour failures (warn color when > 0) */}
      <span
        data-testid={`job-group-fail-${q.kind}`}
        className={cn("inline-flex items-center gap-1 tabular-nums", hasFails && "text-destructive")}
        title="failed in the last hour"
      >
        {hasFails && <AlertTriangle className="h-3 w-3" />}
        {q.failedLastHour} failed
      </span>

      {/* trailing-hour throughput */}
      <span className="tabular-nums" title="completed in the last hour">
        {q.doneLastHour}/h
      </span>

      {/* last activity */}
      <span
        className="tabular-nums"
        title={q.lastRunAt ? new Date(q.lastRunAt).toLocaleString() : undefined}
      >
        last {q.lastRunAt ? relativeTime(q.lastRunAt) : "never"}
      </span>
    </div>
  );
}

function KindGroup({
  q,
  onSelect,
}: {
  q: AdminJobQueue;
  onSelect: (job: AdminJob) => void;
}) {
  const qc = useQueryClient();
  const [expanded, setExpanded] = useState(false);
  const [page, setPage] = useState(1);

  const {
    data: rows,
    isLoading,
    isError,
  } = useQuery({
    queryKey: qk.adminJobs("any", q.kind, 200),
    queryFn: () => api.getAdminJobs({ kind: q.kind, limit: 200 }),
    enabled: expanded,
    refetchInterval: expanded ? 5000 : false,
  });

  const invalidate = () => qc.invalidateQueries({ queryKey: qk.adminJobsPrefix() });

  const retry = useMutation({
    mutationFn: (id: number) => api.retryAdminJob(id),
    onSuccess: (res) => {
      toast.success(`Re-enqueued job #${res.id}`);
      invalidate();
    },
    onError: (e) =>
      toast.error(e instanceof ApiError ? `Retry failed: ${e.message}` : "Retry failed"),
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
      invalidate();
    },
    onError: (e) =>
      toast.error(e instanceof ApiError ? `Cancel failed: ${e.message}` : "Cancel failed"),
  });

  const clearOne = useMutation({
    mutationFn: (id: number) => api.deleteJobLogs(id),
    onSuccess: (_res, id) => {
      toast.success(`Cleared stored logs for job #${id}`);
      invalidate();
    },
    onError: (e) =>
      toast.error(e instanceof ApiError ? `Clear failed: ${e.message}` : "Clear failed"),
  });

  const clearKind = useMutation({
    mutationFn: () => api.clearJobLogs({ kind: q.kind }),
    onSuccess: (res) => {
      toast.success(`Cleared ${res.deleted} stored ${q.kind} log${res.deleted === 1 ? "" : "s"}`);
      invalidate();
    },
    onError: (e) =>
      toast.error(e instanceof ApiError ? `Clear failed: ${e.message}` : "Clear failed"),
  });

  const onClearKind = () => {
    if (
      window.confirm(
        `Delete stored logs for every ${q.kind} job? Job history is kept — only the saved log streams are removed.`,
      )
    ) {
      clearKind.mutate();
    }
  };

  const total = rows?.length ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));
  const safePage = Math.min(page, totalPages);
  const pageRows = rows ? rows.slice((safePage - 1) * PAGE_SIZE, safePage * PAGE_SIZE) : [];

  const failing = q.failedLastHour > 0;
  const running = q.running > 0;

  return (
    <div data-testid={`job-group-${q.kind}`} className="border-b border-border/60 last:border-b-0">
      {/* Header row: expand toggle + kind + inline stats + clear-kind. */}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 px-3 py-2.5">
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="flex min-w-0 flex-1 items-center gap-2 text-left"
          aria-expanded={expanded}
          title={expanded ? `Collapse ${q.kind}` : `Expand ${q.kind}`}
        >
          <span className="flex h-4 w-4 shrink-0 items-center justify-center text-muted-foreground">
            {expanded ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
          </span>
          <span
            data-testid={`job-group-dot-${q.kind}`}
            className={cn(
              "h-2 w-2 shrink-0 rounded-full",
              failing
                ? "bg-destructive"
                : running
                  ? "bg-primary animate-pulse"
                  : "bg-muted-foreground/40",
            )}
          />
          <span className="truncate font-mono text-sm font-medium text-foreground">{q.kind}</span>
        </button>
        <KindStats q={q} />
        <Button
          variant="ghost"
          size="sm"
          className="h-7 shrink-0 px-2 text-muted-foreground hover:text-destructive"
          onClick={onClearKind}
          disabled={clearKind.isPending}
          title={`Clear stored logs for all ${q.kind} jobs`}
          aria-label={`clear ${q.kind} logs`}
        >
          <Trash2 className={cn("h-3.5 w-3.5", clearKind.isPending && "animate-pulse")} />
        </Button>
      </div>

      {/* Expanded: this kind's recent runs, paginated in place. */}
      {expanded && (
        <div className="bg-muted/20 px-3 pb-3">
          {isError ? (
            <p className="py-4 text-center text-sm text-destructive">Failed to load runs.</p>
          ) : isLoading || !rows ? (
            <div className="space-y-1.5 py-3" aria-busy="true">
              {[0, 1, 2].map((i) => (
                <div key={i} className="h-6 animate-pulse rounded bg-muted/60" />
              ))}
            </div>
          ) : rows.length === 0 ? (
            <EmptyState
              icon={ListChecks}
              title="No runs yet"
              description={`Nothing of kind ${q.kind} has been queued. Trigger one from the Schedules panel below.`}
            />
          ) : (
            <div className="overflow-x-auto rounded-md border border-border/60 bg-card">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-16">ID</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Attempts</TableHead>
                    <TableHead>Created</TableHead>
                    <TableHead>Duration</TableHead>
                    <TableHead className="text-right" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {pageRows.map((job) => (
                    <RunRow
                      key={job.id}
                      job={job}
                      onSelect={() => onSelect(job)}
                      retrying={retry.isPending && retry.variables === job.id}
                      onRetry={() => retry.mutate(job.id)}
                      cancelling={cancel.isPending && cancel.variables === job.id}
                      onCancel={() => cancel.mutate(job.id)}
                      clearing={clearOne.isPending && clearOne.variables === job.id}
                      onClearLogs={() => clearOne.mutate(job.id)}
                    />
                  ))}
                </TableBody>
              </Table>
              {total > PAGE_SIZE && (
                <div className="flex items-center justify-end gap-2 border-t border-border/60 px-3 py-2 text-xs text-muted-foreground">
                  <span className="tabular-nums">
                    Page {safePage} / {totalPages}
                  </span>
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-6"
                    disabled={safePage <= 1}
                    onClick={() => setPage((p) => Math.max(1, p - 1))}
                  >
                    Prev
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-6"
                    disabled={safePage >= totalPages}
                    onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                  >
                    Next
                  </Button>
                </div>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// The grouped jobs section: the queue overview + job history fused into one
// collapsible-by-kind table. Server sorts kinds most-active first.
function GroupedJobs() {
  const qc = useQueryClient();
  const [selected, setSelected] = useState<AdminJob | null>(null);

  const { data: queues, isLoading, isError } = useQuery({
    queryKey: qk.adminJobQueues(),
    queryFn: () => api.getJobQueues(),
    refetchInterval: 5000,
  });

  const clearAll = useMutation({
    mutationFn: () => api.clearJobLogs(),
    onSuccess: (res) => {
      toast.success(`Cleared ${res.deleted} stored log${res.deleted === 1 ? "" : "s"}`);
      qc.invalidateQueries({ queryKey: qk.adminJobsPrefix() });
    },
    onError: (e) =>
      toast.error(e instanceof ApiError ? `Clear failed: ${e.message}` : "Clear failed"),
  });

  const onClearAll = () => {
    if (
      window.confirm(
        "Delete ALL stored job logs? Job history is kept — only the saved log streams are removed.",
      )
    ) {
      clearAll.mutate();
    }
  };

  return (
    <section className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="flex items-center gap-2 font-mono text-xs font-semibold uppercase tracking-widest text-muted-foreground">
          <ListChecks className="h-4 w-4 text-primary" />
          Jobs
        </h2>
        <Button
          variant="outline"
          size="sm"
          onClick={onClearAll}
          disabled={clearAll.isPending}
          title="Delete every stored job-log stream (job history is kept)"
        >
          <Trash2 className={cn("h-3.5 w-3.5", clearAll.isPending && "animate-pulse")} />
          Clear all logs
        </Button>
      </div>

      <Card>
        <CardContent className="p-0">
          {isError ? (
            <p className="p-6 text-sm text-muted-foreground">
              Queue stats are unavailable (the jobs subsystem may be disabled).
            </p>
          ) : isLoading || !queues ? (
            <div className="space-y-px p-3" aria-busy="true">
              {[0, 1, 2, 3].map((i) => (
                <div key={i} className="h-10 animate-pulse rounded bg-muted/40" />
              ))}
            </div>
          ) : queues.length === 0 ? (
            <EmptyState
              icon={Gauge}
              title="No job kinds registered"
              description="Nothing is wired to the background-job subsystem yet."
            />
          ) : (
            <div>
              {queues.map((q) => (
                <KindGroup key={q.kind} q={q} onSelect={setSelected} />
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      <JobDetailSheet job={selected} onOpenChange={(open) => !open && setSelected(null)} />
    </section>
  );
}

// ── reading-steps panel ─────────────────────────────────────────────────────

// On-demand triggers for the catalyst-books pipeline kinds, scoped to the
// current (admin) user. Each enqueues a worker job and returns a jobId; we
// surface it via toast. Gated on books_enabled — the whole panel is inert per
// deployment, mirroring the settings cards. Invalidates the jobs prefix so the
// table above reflects the freshly-queued run.
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
    mutationFn: (force?: boolean) => api.matchHardcover({ force }),
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
  // When checked, a Hardcover match run re-checks EVERY book (?force=1), ignoring
  // the 30-day negative-cache skip — for after you curate on Hardcover.
  const [forceMatch, setForceMatch] = useState(false);

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
              onClick={() => (key === "match" ? hardcoverMatch.mutate(forceMatch) : m.mutate())}
              disabled={m.isPending}
              title={`Queue a ${label} run for your account`}
            >
              <Icon className="h-3.5 w-3.5" />
              {m.isPending ? "Starting…" : label}
            </Button>
          ))}
          <label
            className="flex cursor-pointer select-none items-center gap-1.5 pl-1 text-xs text-muted-foreground"
            title="Force: re-check every book, ignoring the 30-day 'no confident match' skip window. Use after curating on Hardcover."
          >
            <input
              type="checkbox"
              className="h-3.5 w-3.5 accent-primary"
              checked={forceMatch}
              onChange={(e) => setForceMatch(e.target.checked)}
            />
            force re-match
          </label>
        </div>
        <p className="mt-3 text-xs text-muted-foreground">
          On-demand pipeline steps for your own account. Each queues a background job — watch it
          land in the table above.
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
        e instanceof ApiError ? `Couldn't run ${kind}: ${e.message}` : `Couldn't run ${kind}`,
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
          <p className="px-4 pb-4 text-sm text-destructive">Failed to load schedules.</p>
        ) : isLoading || !schedules ? (
          <div className="space-y-2 px-4 pb-4" aria-busy="true">
            {[0, 1, 2].map((i) => (
              <div key={i} className="h-10 animate-pulse rounded-md bg-muted/50" />
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
                      title={s.lastRun ? new Date(s.lastRun).toLocaleString() : undefined}
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
      <SheetContent side="right" className="flex w-full flex-col gap-0 sm:max-w-2xl">
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

// ── tab ─────────────────────────────────────────────────────────────────────

export function JobsTab() {
  return (
    <div className="max-w-6xl space-y-6">
      <GroupedJobs />
      <ReadingStepsPanel />
      <SchedulesPanel />
    </div>
  );
}
