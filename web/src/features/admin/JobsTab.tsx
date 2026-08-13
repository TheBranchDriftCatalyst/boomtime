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
  BookOpen,
  CalendarClock,
  DownloadCloud,
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
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/table";
import { EmptyState } from "@/components/EmptyState";
import { api, ApiError } from "@/lib/api";
import { usePublicConfig } from "@/lib/usePublicConfig";
import { qk } from "@/lib/queryKeys";
import { relativeTime } from "@/lib/sourceStatus";
import { cn } from "@/lib/utils";
import type { AdminJob, AdminJobStatus } from "@/types/api";

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
];

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
  onRetry,
  retrying,
}: {
  job: AdminJob;
  onRetry: () => void;
  retrying: boolean;
}) {
  return (
    <TableRow>
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
            onClick={onRetry}
            disabled={retrying}
            title={`Re-enqueue job #${job.id}`}
          >
            <RotateCcw className={cn("h-3.5 w-3.5", retrying && "animate-spin")} />
            Retry
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
}: {
  status: string;
  kind: string;
}) {
  const qc = useQueryClient();
  const trimmedKind = kind.trim();
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
              ) : isLoading || !jobs ? (
                <SkeletonRows />
              ) : jobs.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={JOB_COLS} className="p-0">
                    <EmptyState
                      icon={ListChecks}
                      title="No jobs match"
                      description={
                        status !== "any" || trimmedKind
                          ? "No jobs match the current filters. Clear them, or trigger a run from the Schedules panel above."
                          : "Nothing has been queued yet. Trigger a run from the Schedules panel above."
                      }
                    />
                  </TableCell>
                </TableRow>
              ) : (
                jobs.map((job) => (
                  <JobRow
                    key={job.id}
                    job={job}
                    retrying={retry.isPending && retry.variables === job.id}
                    onRetry={() => retry.mutate(job.id)}
                  />
                ))
              )}
            </TableBody>
          </Table>
        </div>
      </CardContent>
    </Card>
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

  return (
    <div className="max-w-6xl space-y-6">
      <ReadingStepsPanel />
      <SchedulesPanel />

      <section className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="flex items-center gap-2 font-mono text-xs font-semibold uppercase tracking-widest text-muted-foreground">
            <ListChecks className="h-4 w-4 text-primary" />
            Jobs
          </h2>
          <div className="flex items-center gap-2">
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

        <JobsPanel status={status} kind={kind} />
      </section>
    </div>
  );
}
