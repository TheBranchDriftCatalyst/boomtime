// JobsTab — Admin > Jobs (boom-hney). Operator view of the background-job
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
import { useCallback, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AdminTabShell } from "@shared/shared/admin/AdminTabShell";
import { usePageActions } from "@shared/layout/PageActionsSlot";
import { GroupableExplorer } from "@shared/features/explorer/GroupableExplorer";
import { GroupByBar } from "@shared/features/explorer/GroupByBar";
import type { Column } from "@shared/features/explorer/types";
import {
  JOB_AXES,
  JOB_WINDOW,
  makeJobsExplorerConfig,
} from "@shared/features/admin/jobsExplorerConfig";
import {
  AlertTriangle,
  Ban,
  ChevronRight,
  Gauge,
  Layers,
  Play,
  RotateCcw,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  Card,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/sheet";
import { EmptyState } from "@shared/components/EmptyState";
import { JobLogStream } from "@shared/features/logs/JobLogStream";
import { api, ApiError } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import { relativeTime } from "@shared/lib/sourceStatus";
import { cn } from "@shared/lib/utils";
import type {
  AdminJob,
  AdminJobChain,
  AdminJobQueue,
  AdminJobStatus,
} from "@shared/types/api";

// ── formatting helpers ──────────────────────────────────────────────────────

// Forward-looking relative label, e.g. "in 6h", "in 30m", "now" (for a fire
// time already elapsed — the scheduler just hasn't ticked yet).
// Compact past-relative stamp for the run log. Deliberately terse — this sits
// in a dense table column, so "3m" beats "3 minutes ago" at a glance and the
// exact timestamp is one hover away in the title attribute.
function relativePast(ts: string): string {
  const ms = Date.now() - new Date(ts).getTime();
  if (!Number.isFinite(ms)) return "—";
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.round(h / 24)}d`;
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

// ── health strip ────────────────────────────────────────────────────────────

// The one thing an operator opening this page during an incident needs to know,
// answered before they scan anything: is something broken, is something stuck.
//
// Previously that question had no answer above the fold — failure counts existed
// only as small per-kind badges inside the list, so "is anything failing?"
// required reading every row. This states it once, in words, and says nothing
// when there is nothing to say.
//
// Derived entirely from the queues payload already on screen; no extra request.
function HealthStrip({ queues }: { queues: AdminJobQueue[] }) {
  const failing = queues.filter((q) => q.failedLastHour > 0);
  const atCap = queues.filter((q) => q.maxConcurrency > 0 && q.running >= q.maxConcurrency);
  const queued = queues.reduce((n, q) => n + q.queued, 0);
  const running = queues.reduce((n, q) => n + q.running, 0);

  // Backed up = at capacity AND waiting. At capacity alone is a saturated
  // worker doing its job; only a backlog behind it is worth an operator's
  // attention, which is the same distinction KindStats draws per kind.
  const backedUp = atCap.filter((q) => q.queued > 0);
  const healthy = failing.length === 0 && backedUp.length === 0;

  return (
    <div
      data-testid="jobs-health-strip"
      className={cn(
        "flex flex-wrap items-center gap-x-4 gap-y-1.5 border-b px-3 py-2.5 text-sm",
        healthy ? "border-border/60 text-muted-foreground" : "border-destructive/30 bg-destructive/5",
      )}
    >
      {healthy ? (
        <span className="flex items-center gap-2">
          <span className="h-2 w-2 rounded-full bg-muted-foreground/40" />
          {running > 0 ? `${running} running` : "Idle"}
          {queued > 0 && <span className="opacity-70">· {queued} queued</span>}
          <span className="opacity-70">· no failures in the last hour</span>
        </span>
      ) : (
        <>
          {failing.length > 0 && (
            <span className="flex items-center gap-1.5 font-medium text-destructive">
              <AlertTriangle className="h-3.5 w-3.5" />
              {failing.length} kind{failing.length === 1 ? "" : "s"} failing
              <span className="font-normal opacity-80">
                ({failing.map((q) => q.kind).slice(0, 3).join(", ")}
                {failing.length > 3 ? ` +${failing.length - 3}` : ""})
              </span>
            </span>
          )}
          {backedUp.length > 0 && (
            <span className="flex items-center gap-1.5 text-foreground">
              <Layers className="h-3.5 w-3.5" />
              {backedUp.length} backed up
            </span>
          )}
          <span className="text-muted-foreground">
            {running} running · {queued} queued
          </span>
        </>
      )}
    </div>
  );
}

// ── grouped jobs: one kind = one collapsible header carrying its live stats ──

// The runs table inside an expanded kind. Newest-first, and page it here in the
// FE so an active kind can't balloon the tab — mirrors the Books explorer's
// leaf-page pagination (a bounded fetch, sliced Prev/Next).
// ── chains ──────────────────────────────────────────────────────────────────

// A chain is a kind COMPOSED of other kinds: books-sync-all is one job that
// internally runs seven stages in dependency order, and every stage is itself a
// registered kind. Deleting the old hardcoded books panel lost the ability to
// SEE that composition, which is the one thing a flat per-kind list genuinely
// cannot express — ordering.
//
// Rendered from whatever the registry declares (Registry.SetChain), so a new
// pipeline, or a new stage in an existing one, appears here with no frontend
// change. Nothing below names a pipeline.
function ChainsStrip({
  chains,
  onRun,
  running,
}: {
  chains: AdminJobChain[];
  onRun: (kind: string) => void;
  running: string | null;
}) {
  if (chains.length === 0) return null;
  return (
    <div data-testid="jobs-chains" className="border-b border-border/60 px-3 py-2.5">
      {chains.map((c) => (
        <div key={c.kind} className="flex flex-wrap items-center gap-x-2 gap-y-1.5">
          <Button
            variant="ghost"
            size="sm"
            className="h-7 shrink-0 gap-1.5 px-2 font-mono text-xs"
            onClick={() => onRun(c.kind)}
            disabled={running === c.kind}
            title={`Run the whole ${c.kind} chain — FLEET-WIDE, for every eligible user`}
            aria-label={`run chain ${c.kind}`}
          >
            <Play className={cn("h-3.5 w-3.5", running === c.kind && "animate-pulse")} />
            {c.kind}
          </Button>
          {/* The steps, in run order. Each is a registered kind, so each is
              independently runnable — which is what the old panel's per-step
              buttons were for, minus the hardcoding. */}
          <div className="flex flex-wrap items-center gap-1">
            {c.steps.map((step, i) => (
              <span key={step} className="flex items-center gap-1">
                {i > 0 && <ChevronRight className="h-3 w-3 shrink-0 text-muted-foreground/50" />}
                <button
                  type="button"
                  onClick={() => onRun(step)}
                  disabled={running === step}
                  title={`Run just ${step} — FLEET-WIDE, for every eligible user`}
                  aria-label={`run step ${step}`}
                  className={cn(
                    "rounded border border-border/60 px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground transition-colors",
                    "hover:border-border hover:bg-muted hover:text-foreground",
                    running === step && "animate-pulse opacity-60",
                  )}
                >
                  {step.replace(/^books-/, "")}
                </button>
              </span>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

// ── the console ─────────────────────────────────────────────────────────────

function GroupedJobs() {
  const qc = useQueryClient();
  const [selected, setSelected] = useState<AdminJob | null>(null);
  const [groupBy, setGroupBy] = useState<string[]>(["kind"]);

  const {
    data: overview,
    isLoading: overviewLoading,
    isError,
  } = useQuery({
    queryKey: qk.adminJobQueues(),
    queryFn: () => api.getJobQueues(),
    refetchInterval: 5000,
  });

  // ONE fetch of recent runs feeds the whole table; grouping happens in memory
  // (see jobsExplorerConfig). Polls on the same cadence as the queue stats so
  // the strip and the rows never disagree about what just happened.
  const { data: jobs, isLoading: jobsLoading } = useQuery({
    queryKey: qk.adminJobs("any", "", JOB_WINDOW),
    queryFn: () => api.getAdminJobs({ limit: JOB_WINDOW }),
    refetchInterval: 5000,
  });

  const invalidate = useCallback(
    () => qc.invalidateQueries({ queryKey: qk.adminJobsPrefix() }),
    [qc],
  );

  const [runningKind, setRunningKind] = useState<string | null>(null);
  const trigger = useMutation({
    mutationFn: (kind: string) => {
      setRunningKind(kind);
      return api.triggerAdminJob(kind);
    },
    onSuccess: (res, kind) => {
      toast.success(`Enqueued ${kind} — job #${res.id}`);
      invalidate();
    },
    onError: (e, kind) =>
      toast.error(e instanceof Error ? e.message : `Could not enqueue ${kind}`),
    onSettled: () => setRunningKind(null),
  });

  const retry = useMutation({
    mutationFn: (id: number) => api.retryAdminJob(id),
    onSuccess: (res) => {
      toast.success(`Re-enqueued job #${res.id}`);
      invalidate();
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Retry failed"),
  });

  const cancel = useMutation({
    mutationFn: (id: number) => api.cancelJob(id),
    onSuccess: () => {
      toast.success("Job cancelled");
      invalidate();
    },
    onError: (e) => toast.error(e instanceof Error ? e.message : "Cancel failed"),
  });

  const clearAll = useMutation({
    mutationFn: () => api.clearJobLogs(),
    onSuccess: (res) => {
      toast.success(`Cleared ${res.deleted} stored log${res.deleted === 1 ? "" : "s"}`);
      invalidate();
    },
    onError: (e) =>
      toast.error(e instanceof ApiError ? `Clear failed: ${e.message}` : "Clear failed"),
  });

  const onClearAll = useCallback(() => {
    if (
      window.confirm(
        "Delete ALL stored job logs? Job history is kept — only the saved log streams are removed.",
      )
    ) {
      clearAll.mutate();
    }
  }, [clearAll]);

  // "Clear all logs" is the TAB's destructive action, so it rides the
  // page-actions slot into the shell header rather than sitting in the body.
  const headerActions = useMemo(
    () => (
      <Button
        variant="outline"
        size="sm"
        onClick={onClearAll}
        disabled={clearAll.isPending}
        className="gap-1.5"
      >
        <Trash2 className={cn("h-3.5 w-3.5", clearAll.isPending && "animate-pulse")} />
        Clear all logs
      </Button>
    ),
    [onClearAll, clearAll.isPending],
  );
  usePageActions(headerActions);

  // Leaf columns. Defined here rather than in the config because they need the
  // presentation helpers and the row actions need this component's mutations;
  // the config owns grouping, this owns how a run looks.
  const columns = useMemo<Column<AdminJob>[]>(
    () => [
      {
        id: "id",
        header: "#",
        get: (r) => r.id,
        render: (r) => <span className="font-mono text-xs text-muted-foreground">#{r.id}</span>,
      },
      {
        id: "status",
        header: "Status",
        get: (r) => r.status,
        render: (r) => <StatusBadge status={r.status} />,
      },
      {
        id: "owner",
        header: "Owner",
        get: (r) => r.owner,
        render: (r) =>
          r.owner ? (
            <span className="font-mono text-xs text-muted-foreground">{r.owner}</span>
          ) : (
            // Not missing data — a fleet-wide run genuinely has no single owner.
            <span className="text-[11px] uppercase tracking-wide text-muted-foreground/60">
              fleet
            </span>
          ),
      },
      {
        id: "attempts",
        header: "Attempts",
        get: (r) => r.attempts,
        render: (r) => (
          <span
            className={cn(
              "font-mono text-xs",
              r.attempts >= r.maxAttempts && r.status === "failed"
                ? "text-destructive"
                : "text-muted-foreground",
            )}
          >
            {r.attempts}/{r.maxAttempts}
          </span>
        ),
      },
      {
        id: "duration",
        header: "Duration",
        get: (r) =>
          r.startedAt && r.finishedAt
            ? new Date(r.finishedAt).getTime() - new Date(r.startedAt).getTime()
            : 0,
        render: (r) => (
          <span className="font-mono text-xs text-muted-foreground">
            {jobDuration(r.startedAt, r.finishedAt)}
          </span>
        ),
      },
      {
        id: "when",
        header: "When",
        get: (r) => r.createdAt,
        render: (r) => (
          <span className="text-xs text-muted-foreground" title={new Date(r.createdAt).toLocaleString()}>
            {relativePast(r.createdAt)}
          </span>
        ),
      },
      {
        id: "error",
        header: "Error",
        get: (r) => r.error,
        cellClassName: "max-w-[36rem]",
        cellTitle: (r) => r.error || undefined,
        render: (r) =>
          r.error ? (
            <span className="line-clamp-1 text-xs text-destructive">{r.error}</span>
          ) : (
            <span className="text-xs text-muted-foreground/40">—</span>
          ),
      },
    ],
    [],
  );

  const rowActions = useCallback(
    (r: AdminJob) => (
      <div className="flex items-center gap-0.5">
        {r.status === "failed" && (
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              retry.mutate(r.id);
            }}
            title={`Re-enqueue job #${r.id}`}
            aria-label={`retry job ${r.id}`}
            className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
          >
            <RotateCcw className="h-3.5 w-3.5" />
          </button>
        )}
        {(r.status === "running" || r.status === "queued") && (
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              cancel.mutate(r.id);
            }}
            title={`Cancel job #${r.id}`}
            aria-label={`cancel job ${r.id}`}
            className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-destructive"
          >
            <Ban className="h-3.5 w-3.5" />
          </button>
        )}
      </div>
    ),
    [retry, cancel],
  );

  const queues = overview?.queues ?? [];

  const config = useMemo(
    () =>
      makeJobsExplorerConfig({
        rows: jobs ?? [],
        knownKinds: queues.map((q) => q.kind),
        columns,
        onRowSelect: setSelected,
        rowActions,
        empty: (
          <EmptyState
            icon={Gauge}
            title="No runs yet"
            description="Nothing has been queued. Run a kind from its row, or a whole chain above."
          />
        ),
      }),
    [jobs, queues, columns, rowActions],
  );

  const chains = overview?.chains ?? [];

  return (
    // h-full + flex so the run log FILLS the viewport instead of floating in it.
    // A console that stops two-thirds up the page wastes exactly the space an
    // operator wants for history.
    <section className="flex h-full min-h-0 flex-col">
      {/* ONE surface. Health, chain, controls and table used to be three
          concentric bordered boxes inside the page shell — a lot of chrome and
          padding spent saying nothing. They are now bands on a single panel,
          separated by hairlines. */}
      <Card className="flex min-h-0 flex-1 flex-col overflow-hidden">
        {isError ? (
          <p className="p-6 text-sm text-muted-foreground">
            Queue stats are unavailable (the jobs subsystem may be disabled).
          </p>
        ) : overviewLoading || jobsLoading ? (
          <div className="space-y-px p-3" aria-busy="true">
            {[0, 1, 2, 3].map((i) => (
              <div key={i} className="h-10 animate-pulse rounded bg-muted/40" />
            ))}
          </div>
        ) : (
          <>
            <HealthStrip queues={queues} />
            <ChainsStrip
              chains={chains}
              onRun={(k) => trigger.mutate(k)}
              running={runningKind}
            />
            {/* Controls inline on one band rather than in their own box. */}
            <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-border/60 px-3 py-2">
              <GroupByBar axes={JOB_AXES} groupBy={groupBy} onChange={setGroupBy} />
            </div>
            {/* The table is the only thing that scrolls, so the bands above stay
                pinned while history runs long. */}
            <div className="min-h-0 flex-1 overflow-auto px-3 pb-2">
              <GroupableExplorer
                config={config}
                groupBy={groupBy}
                onGroupByChange={setGroupBy}
                resetKey={`jobs:${jobs?.length ?? 0}`}
                hideGroupByBar
                bare
              />
            </div>
            {(jobs?.length ?? 0) >= JOB_WINDOW && (
              <p className="border-t border-border/60 px-3 py-1.5 text-[11px] text-muted-foreground">
                Showing the most recent {JOB_WINDOW} runs.
              </p>
            )}
          </>
        )}
      </Card>

      <JobDetailSheet job={selected} onOpenChange={(open) => !open && setSelected(null)} />
    </section>
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
  // ONE panel, keyed on the job KIND — which is already the organising unit in
  // the data model (/queues unions in registry.Kinds(), so a registered-but-idle
  // kind still gets a row).
  //
  // This used to be three co-equal stacked tables: grouped jobs, a hardcoded
  // five-button books panel, and a separate schedules table. That forced an
  // operator to learn three row idioms and to cross-reference two of them to
  // answer one question. The schedule now rides on its kind's row, and the
  // books panel is gone: every kind gets a run button generated from the
  // registry, so a new job kind is runnable with no UI change at all.
  return (
    <AdminTabShell bodyClassName="space-y-6">
      <GroupedJobs />
    </AdminTabShell>
  );
}
