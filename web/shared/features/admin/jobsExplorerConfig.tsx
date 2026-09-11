// jobsExplorerConfig — the Jobs console as ONE groupable table.
//
// The console used to be three stacked tables plus a list of accordions, which
// is neither a list nor a table: to compare two kinds you expanded both and
// scrolled, and to group by anything other than kind you could not. This drives
// the same GroupableExplorer the Books page uses, so Jobs gets drill-down,
// sortable leaf columns and drag-to-reorder axes for free, and looks like the
// rest of the app instead of like its own thing.
//
// WHY THE SOURCE IS CLIENT-SIDE. Every other explorer domain is backed by the
// cross-domain query DSL, which does the grouping in Postgres. Jobs are not a
// query-DSL domain — the admin API serves a flat, bounded list — so this groups
// in memory over one fetched window. That is an honest fit for an operator
// console looking at recent history, and it costs one request instead of one
// per drill level. The window IS a cap, so the caller shows it (see JOB_WINDOW).
import type React from "react";
import { useCallback, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CalendarClock, Play, UserRound } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dropdown-menu";
import { toast } from "sonner";
import { cn } from "@shared/lib/utils";

import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";

import type {
  Axis,
  Column,
  DomainConfig,
  DrillPath,
  GroupPage,
  GroupStats,
  LeafResult,
} from "@shared/features/explorer/types";
import type { GroupAction } from "@shared/features/explorer/types";
import type { AdminJob } from "@shared/types/api";

// How many recent jobs the table works over. Grouping is in-memory, so this is
// the real limit on what the console can see — surfaced in the UI rather than
// silently truncating, because "no failures" and "no failures in the last 500"
// are very different claims to make to someone debugging an incident.
export const JOB_WINDOW = 500;

export const JOB_LEAF_PAGE_SIZE = 25;

// The groupable dimensions. Ordinary fields of a job row, so adding one is a
// line here plus a case in groupValue.
export const JOB_AXES: Axis[] = [
  { id: "kind", label: "Kind" },
  { id: "status", label: "Status" },
  { id: "outcome", label: "Outcome" },
  { id: "owner", label: "Owner" },
];

// outcome collapses the five statuses into the question an operator actually
// asks — did it work — so "group by outcome" gives a three-row triage view
// instead of a five-row status census.
function outcomeOf(r: AdminJob): string {
  switch (r.status) {
    case "failed":
      return "failed";
    case "done":
      return "succeeded";
    default:
      return "in flight";
  }
}

function groupValue(r: AdminJob, axis: string): string {
  switch (axis) {
    case "kind":
      return r.kind;
    case "status":
      return r.status;
    case "outcome":
      return outcomeOf(r);
    case "owner":
      // "" is a fleet-wide run, which is a real and distinct answer to "whose
      // work is this" — not a missing value. Naming it beats rendering "(none)".
      return r.owner || "fleet-wide";
    default:
      return "";
  }
}

// A row belongs under a drill path when it matches every step of it.
function matchesPath(r: AdminJob, path: DrillPath): boolean {
  return path.every((step) => groupValue(r, step.dim) === step.value);
}

export function makeJobsExplorerConfig(opts: {
  rows: AdminJob[];
  // Every REGISTERED kind, from /queues (which unions registry.Kinds()). Without
  // this the table would only list kinds that happen to have a run inside the
  // window — so a kind that has never run would have no row, and therefore no
  // run button, which is precisely the kind an operator most wants to start.
  knownKinds?: string[];
  columns: Column<AdminJob>[];
  onRowSelect?: (r: AdminJob) => void;
  rowActions?: (r: AdminJob) => React.ReactNode;
  empty?: React.ReactNode;
}): DomainConfig<AdminJob> {
  const { rows, columns, knownKinds = [], onRowSelect, rowActions, empty } = opts;

  const source = {
    fetchGroup: async (path: DrillPath, axis: string): Promise<GroupPage> => {
      const buckets = new Map<string, GroupStats>();
      for (const r of rows) {
        if (!matchesPath(r, path)) continue;
        const v = groupValue(r, axis);
        const st = buckets.get(v) ?? { count: 0, failed: 0, running: 0, avgMs: 0, _finished: 0 };
        st.count += 1;
        if (r.status === "failed") st.failed += 1;
        if (r.status === "running") st.running += 1;
        // Mean duration over runs that actually finished. Accumulated as a sum
        // here and divided below.
        //
        // Computed from the SAME rows as every other number on the line, rather
        // than read off the /queues payload which also carries an avgDurationMs:
        // that one is scoped to the last HOUR while this table is the last 500
        // RUNS, so mixing them would put two numbers from two different windows
        // on one row and let them disagree in front of an operator.
        if (r.startedAt && r.finishedAt) {
          st.avgMs += new Date(r.finishedAt).getTime() - new Date(r.startedAt).getTime();
          st._finished = (st._finished ?? 0) + 1;
        }
        buckets.set(v, st);
      }
      // FAILURES FIRST, then volume. The default sort of an operator console
      // should put what is broken at the top; sorting purely by count buries a
      // kind that failed twice under one that succeeded four hundred times.
      // Union in the registered-but-idle kinds at the ROOT of a kind grouping,
      // at zero depth. Only at the root and only for this axis: a drilled path
      // is asking "what ran under here", and answering with kinds that did not
      // run would be a lie.
      if (axis === "kind" && path.length === 0) {
        for (const k of knownKinds) {
          if (!buckets.has(k)) {
            buckets.set(k, { count: 0, failed: 0, running: 0, avgMs: 0, _finished: 0 });
          }
        }
      }
      for (const st of buckets.values()) {
        st.avgMs = st._finished ? Math.round(st.avgMs / st._finished) : 0;
        delete st._finished;
      }
      const groups = [...buckets.entries()]
        .map(([value, stats]) => ({ value: value === "" ? null : value, stats }))
        .sort(
          (a, b) => b.stats.failed - a.stats.failed || b.stats.count - a.stats.count,
        );
      return { groups, truncated: false };
    },

    fetchLeaf: async (
      path: DrillPath,
      page: number,
      pageSize: number,
    ): Promise<LeafResult<AdminJob>> => {
      const all = rows.filter((r) => matchesPath(r, path));
      const start = (page - 1) * pageSize;
      return {
        rows: all.slice(start, start + pageSize),
        total: all.length,
        page,
        limit: pageSize,
      };
    },
  };

  return {
    axes: JOB_AXES,
    // Kind first: it is the unit the rest of the subsystem is organised around
    // (the registry, concurrency caps, schedules and chains are all per-kind).
    // Drag another axis in to slice it differently; drop them all for a flat
    // run log, which is the natural "what just happened" view.
    defaultGroupBy: ["kind"],
    columns,
    // Shown inline on every group row, so a collapsed kind still says whether
    // it is healthy — that was the whole job of the old accordion header.
    rollups: [
      { id: "failed", label: "failed" },
      { id: "running", label: "running" },
      {
        id: "avgMs",
        label: "avg",
        // 0 means nothing in the window finished — an em dash is honest where
        // "0ms" would read as "instant".
        format: (n) =>
          n === 0 ? "—" : n < 1000 ? `${n}ms` : n < 60_000 ? `${(n / 1000).toFixed(1)}s` : `${Math.round(n / 60_000)}m`,
      },
    ],
    source,
    rowKey: (r) => String(r.id),
    leafPageSize: JOB_LEAF_PAGE_SIZE,
    labels: {
      leafGroup: "Runs",
      treeHeader: "Kind / run",
      // No addAxisHint: dropping every axis gives a flat run log rather than a
      // "pick an axis" prompt. For jobs that is a legitimate view, not an
      // unconfigured one.
      loadError: "Failed to load job runs.",
      empty,
    },
    rowActions,
    onRowSelect,
    useGroupDecorator: useJobGroupDecorator,
  };
}


// ── group decoration: cadence + run, on the kind row ────────────────────────

function humanizeInterval(sec: number): string {
  if (sec % 3600 === 0) return `${sec / 3600}h`;
  if (sec % 60 === 0) return `${sec / 60}m`;
  return `${sec}s`;
}

function relativeFuture(ts: string): string {
  const ms = new Date(ts).getTime() - Date.now();
  if (!Number.isFinite(ms)) return "—";
  if (ms <= 0) return "due";
  const s = Math.round(ms / 1000);
  if (s < 60) return `in ${s}s`;
  const m = Math.round(s / 60);
  if (m < 60) return `in ${m}m`;
  return `in ${Math.round(m / 60)}h`;
}

// useJobGroupDecorator hangs the two per-KIND affordances off the group row:
// its cadence, and a button to run it.
//
// A hook rather than a plain function because the explorer's contract makes it
// one — which is exactly what this needs, since the decorator owns its own
// schedules query and its own trigger mutation instead of having them threaded
// down from the tab.
//
// Both only apply when grouping BY KIND. Group by status and a row is "failed",
// which has no cadence and nothing to run; decorating it would be nonsense.
export function useJobGroupDecorator(): GroupAction {
  const qc = useQueryClient();
  const [running, setRunning] = useState<string | null>(null);

  // Slower than the queue poll: an interval and a next-run time move on the
  // order of minutes, so refetching them at queue-depth cadence is waste.
  const { data: schedules } = useQuery({
    queryKey: qk.adminJobSchedules(),
    queryFn: () => api.getAdminJobSchedules(),
    refetchInterval: 30_000,
  });

  // Which kinds are targetable, declared server-side (Registry.SetUserScoped).
  // Same query key the tab already polls, so this is a cache read, not a request.
  const { data: overview } = useQuery({
    queryKey: qk.adminJobQueues(),
    queryFn: () => api.getJobQueues(),
    refetchInterval: 5000,
  });

  // The candidate targets. Disabled accounts are filtered out — enqueueing work
  // for someone who cannot log in is never what an operator meant.
  const { data: usersPayload } = useQuery({
    queryKey: qk.adminUsers(),
    queryFn: () => api.getAdminUsers(),
    staleTime: 60_000,
  });
  const users = (usersPayload?.users ?? []).filter((u) => !u.disabled);

  const trigger = useMutation({
    mutationFn: ({ kind, owner }: { kind: string; owner?: string }) => {
      setRunning(kind);
      return api.triggerAdminJob(kind, owner);
    },
    // The toast says WHO it ran for, because fleet-wide and single-user are very
    // different things to have just done and the button for each is 20px apart.
    onSuccess: (res, { kind, owner }) => {
      toast.success(
        owner
          ? `Enqueued ${kind} for ${owner} — job #${res.id}`
          : `Enqueued ${kind} fleet-wide — job #${res.id}`,
      );
      qc.invalidateQueries({ queryKey: qk.adminJobsPrefix() });
    },
    onError: (e, { kind }) =>
      toast.error(e instanceof Error ? e.message : `Could not enqueue ${kind}`),
    onSettled: () => setRunning(null),
  });

  return useCallback(
    (node) => {
      if (node.axis !== "kind" || !node.value) return {};
      const kind = node.value;
      const sched = (schedules ?? []).find((s) => s.kind === kind);
      const targetable =
        (overview?.queues ?? []).find((q) => q.kind === kind)?.userScoped ?? false;
      return {
        // An unscheduled kind shows nothing rather than "manual": most kinds are
        // enqueue-on-demand, so labelling the absence would be noise on nearly
        // every row.
        badges: sched ? (
          <span
            data-testid={`job-group-schedule-${kind}`}
            className="ml-2 hidden items-center gap-1 whitespace-nowrap text-[11px] font-normal text-muted-foreground sm:inline-flex"
            title={`Runs every ${humanizeInterval(sched.intervalSeconds)}; next ${relativeFuture(sched.nextRun)}`}
          >
            <CalendarClock className="h-3 w-3" />
            {humanizeInterval(sched.intervalSeconds)}
            <span className="opacity-60">· next {relativeFuture(sched.nextRun)}</span>
          </span>
        ) : undefined,
        actions: (
          <span className="flex items-center gap-0.5">
            {/* Fleet-wide stays the PRIMARY action: an operator control should
                act on the system by default, not quietly on one account. */}
            <button
              type="button"
              onClick={(e) => {
                e.stopPropagation();
                trigger.mutate({ kind });
              }}
              disabled={running === kind}
              title={`Run ${kind} now — FLEET-WIDE, for every eligible user`}
              aria-label={`run ${kind}`}
              className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
            >
              <Play className={cn("h-3.5 w-3.5", running === kind && "animate-pulse")} />
            </button>

            {/* Targeting only where it MEANS something. A leader-singleton loop
                or a payload-driven kind ignores the owner, so a picker there
                would be a control that silently does nothing. */}
            {targetable && users.length > 0 && (
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-auto rounded p-1 text-muted-foreground hover:text-foreground"
                    onClick={(e) => e.stopPropagation()}
                    title={`Run ${kind} for one user`}
                    aria-label={`run ${kind} for a user`}
                  >
                    <UserRound className="h-3.5 w-3.5" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" onClick={(e) => e.stopPropagation()}>
                  <DropdownMenuLabel className="font-mono text-xs">{kind}</DropdownMenuLabel>
                  <DropdownMenuSeparator />
                  {users.map((u) => (
                    <DropdownMenuItem
                      key={u.username}
                      onClick={() => trigger.mutate({ kind, owner: u.username })}
                    >
                      {u.username}
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            )}
          </span>
        ),
      };
    },
    [schedules, overview, users, trigger, running],
  );
}
