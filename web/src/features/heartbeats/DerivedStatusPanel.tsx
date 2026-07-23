import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { formatBytes } from "@/lib/utils";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Card, CardContent, CardHeader } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { LabeledStat } from "@/components/LabeledStat";

/**
 * Shows the health of the precomputed derived tables (gap_seconds + the daily
 * rollup) that power the fast stats, warns if they drift out of sync with the
 * raw heartbeats, and offers a one-click resync (full rebuild).
 */
export function DerivedStatusPanel() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: qk.derivedStatus(),
    queryFn: api.getDerivedStatus,
    refetchInterval: 30_000,
  });

  const resync = useMutation({
    mutationFn: api.resyncDerived,
    onSuccess: (s) => {
      qc.setQueryData(qk.derivedStatus(), s);
      // Aggregates were busted server-side; refresh the explorer too.
      qc.invalidateQueries({ queryKey: qk.prefix.hbExploreGroup });
      qc.invalidateQueries({ queryKey: qk.prefix.hbExploreList });
      toast.success(
        s.inSync ? "Derived data resynced — in sync" : "Resynced (still drifting?)",
      );
    },
    onError: () => toast.error("Resync failed"),
  });

  const fmt = (n: number) => n.toLocaleString();
  const hrs = (s: number) => `${(s / 3600).toFixed(1)}h`;
  const inSync = data?.inSync ?? true;

  return (
    <Card className="rounded-lg p-4">
      <CardHeader className="flex-row flex-wrap items-center justify-between gap-3 space-y-0 p-0">
        <div className="flex items-center gap-2">
          {isLoading ? (
            <span className="text-sm text-muted-foreground">Checking derived data…</span>
          ) : inSync ? (
            <span className="flex items-center gap-1.5 text-sm font-medium text-emerald-500">
              <CheckCircle2 className="h-4 w-4" /> Derived data in sync
            </span>
          ) : (
            <span className="flex items-center gap-1.5 text-sm font-medium text-amber-500">
              <AlertTriangle className="h-4 w-4" /> Derived data out of sync
            </span>
          )}
        </div>
        <Button
          variant="outline"
          size="sm"
          onClick={() => resync.mutate()}
          disabled={resync.isPending}
          title="Rebuild gap_seconds + the daily rollup from the raw heartbeats"
        >
          <RefreshCw className={`h-4 w-4 ${resync.isPending ? "animate-spin" : ""}`} />
          {resync.isPending ? "Resyncing…" : "Resync"}
        </Button>
      </CardHeader>

      <CardContent className="p-0">
        {data && (
          <div className="mt-3 grid grid-cols-2 gap-4 sm:grid-cols-4 lg:grid-cols-6">
            <LabeledStat label="Heartbeats" value={fmt(data.heartbeats)} />
            <LabeledStat
              label="gap_seconds"
              value={`${fmt(data.gapPopulated)}${data.gapMissing > 1 ? ` (−${fmt(data.gapMissing)})` : ""}`}
            />
            <LabeledStat label="Rollup rows" value={fmt(data.rollupRows)} />
            <LabeledStat label="Rollup total" value={hrs(data.rollupSeconds)} />
            <LabeledStat label="Raw total" value={hrs(data.rawSeconds)} />
            <LabeledStat
              label="Drift"
              value={
                data.rollupSeconds === data.rawSeconds
                  ? "0s"
                  : `${fmt(Math.abs(data.rollupSeconds - data.rawSeconds))}s`
              }
            />
            <LabeledStat
              label="heartbeats tbl"
              value={formatBytes(data.heartbeatsBytes)}
            />
            <LabeledStat
              label="rollup tbl"
              value={formatBytes(data.rollupBytes)}
            />
            <LabeledStat
              label="Database size"
              value={formatBytes(data.dbBytes)}
            />
          </div>
        )}
        {data && data.heartbeatsIndexes && data.heartbeatsIndexes.length > 0 && (
          <details className="mt-4 rounded border border-border/60 bg-muted/20 p-2 text-xs">
            <summary className="cursor-pointer select-none text-muted-foreground hover:text-foreground">
              Indexes on <code className="font-mono">heartbeats</code>{" "}
              <span className="text-muted-foreground/70">
                ({data.heartbeatsIndexes.length} indexes,{" "}
                {formatBytes(
                  data.heartbeatsIndexes.reduce((s, i) => s + i.bytes, 0),
                )}{" "}
                total)
              </span>
            </summary>
            <ul className="mt-2 grid grid-cols-1 gap-x-4 gap-y-1 sm:grid-cols-2">
              {data.heartbeatsIndexes.map((idx) => {
                const perf =
                  idx.name.includes("_trgm_idx") ||
                  idx.name.includes("_pattern_idx");
                return (
                  <li
                    key={idx.name}
                    className="flex items-center justify-between gap-2 rounded px-1 py-0.5 tabular-nums hover:bg-muted/40"
                  >
                    <span className="flex min-w-0 items-center gap-1.5 truncate">
                      <code className="truncate font-mono text-[11px] text-foreground/90">
                        {idx.name}
                      </code>
                      {perf && (
                        <span
                          className="rounded bg-primary/15 px-1 text-[10px] font-medium uppercase tracking-wide text-primary"
                          title="Perf index for Space regex queries (gaka-o4m)"
                        >
                          perf
                        </span>
                      )}
                    </span>
                    <span className="shrink-0 text-muted-foreground">
                      {formatBytes(idx.bytes)}
                    </span>
                  </li>
                );
              })}
            </ul>
          </details>
        )}
        {!inSync && !isLoading && (
          <p className="mt-2 text-xs text-amber-500/90">
            The rollup total differs from the raw heartbeats (or gaps are
            missing). Click Resync to rebuild the precomputed tables.
          </p>
        )}
      </CardContent>
    </Card>
  );
}
