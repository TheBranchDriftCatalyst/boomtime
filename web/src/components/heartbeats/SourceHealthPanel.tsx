import { useMemo } from "react";
import { Activity, Plug } from "lucide-react";
import { useSourceHealth } from "@/hooks/useSourceHealth";
import {
  deriveSourceStatus,
  relativeTime,
  STATUS_RANK,
  type SourceStatus,
} from "@/lib/sourceStatus";
import type { SourceHealth } from "@/types/api";
import { cn } from "@/lib/utils";

// Synthwave-friendly pill styling per status. active = healthy green,
// idle = calm cyan, stale = amber warning, silent = red alert.
const STATUS_PILL: Record<SourceStatus, string> = {
  active: "border-emerald-500/40 bg-emerald-500/10 text-emerald-400",
  idle: "border-sky-500/40 bg-sky-500/10 text-sky-300",
  stale: "border-amber-500/40 bg-amber-500/10 text-amber-300",
  silent: "border-rose-500/40 bg-rose-500/10 text-rose-300",
};

const STATUS_LABEL: Record<SourceStatus, string> = {
  active: "active",
  idle: "idle",
  stale: "stale",
  silent: "silent",
};

// A source is a (plugin, machine) pair — the compound uniqueness key.
const sourceKey = (s: SourceHealth) => `${s.plugin}@${s.machine}`;

interface Row extends SourceHealth {
  status: SourceStatus;
  rel: string;
}

function SourceRow({ row }: { row: Row }) {
  const key = sourceKey(row);
  return (
    <div className="flex items-center gap-3 border-t border-border/50 py-2 first:border-t-0">
      <Plug className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden />
      <span className="min-w-0 flex-1 truncate text-sm" title={key}>
        <span className="font-mono">{row.plugin}</span>
        <span className="text-muted-foreground"> @ {row.machine}</span>
      </span>
      <span
        className="shrink-0 text-xs tabular-nums text-muted-foreground"
        title={new Date(row.lastSeen).toLocaleString()}
      >
        {row.rel}
      </span>
      <span
        className={cn(
          "shrink-0 rounded-full border px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide",
          STATUS_PILL[row.status],
        )}
        data-testid={`status-${key}`}
      >
        {STATUS_LABEL[row.status]}
      </span>
    </div>
  );
}

/**
 * Compact "Source health" panel: every ingestion source — a (plugin, machine)
 * pair — with its last check-in and a derived active/idle/stale/silent status,
 * so a wakatime plugin that has stopped reporting on a given machine is obvious
 * at a glance. Sorted silent/stale first.
 */
export function SourceHealthPanel() {
  const { data, isLoading, isError } = useSourceHealth();

  const rows = useMemo<Row[]>(() => {
    const now = new Date();
    const src = data?.sources ?? [];
    return src
      .map((s) => ({
        ...s,
        status: deriveSourceStatus(s.lastSeen, now),
        rel: relativeTime(s.lastSeen, now),
      }))
      .sort((a, b) => {
        // Silent/stale first (lower rank first), then oldest check-in first.
        if (STATUS_RANK[a.status] !== STATUS_RANK[b.status]) {
          return STATUS_RANK[a.status] - STATUS_RANK[b.status];
        }
        return new Date(a.lastSeen).getTime() - new Date(b.lastSeen).getTime();
      });
  }, [data]);

  return (
    <div className="rounded-lg border bg-card p-4">
      <div className="mb-2 flex items-center gap-2">
        <Activity className="h-4 w-4 text-primary" aria-hidden />
        <h2 className="text-sm font-medium">Source health</h2>
        <span className="text-xs text-muted-foreground">
          per plugin · machine last check-in
        </span>
      </div>

      {isLoading ? (
        <p className="py-4 text-sm text-muted-foreground">Checking sources…</p>
      ) : isError ? (
        <p className="py-4 text-sm text-destructive">Failed to load source health.</p>
      ) : rows.length === 0 ? (
        <p className="py-4 text-sm text-muted-foreground">
          No ingestion sources yet — point an editor plugin at gakatime to start
          reporting.
        </p>
      ) : (
        <div>
          {rows.map((row) => (
            <SourceRow key={sourceKey(row)} row={row} />
          ))}
        </div>
      )}
    </div>
  );
}
