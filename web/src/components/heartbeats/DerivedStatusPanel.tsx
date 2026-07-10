import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, RefreshCw } from "lucide-react";
import { toast } from "sonner";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";

// Small stat cell for the derived-data health panel.
function Cell({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="font-mono text-sm">{value}</span>
    </div>
  );
}

/**
 * Shows the health of the precomputed derived tables (gap_seconds + the daily
 * rollup) that power the fast stats, warns if they drift out of sync with the
 * raw heartbeats, and offers a one-click resync (full rebuild).
 */
export function DerivedStatusPanel() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ["derived-status"],
    queryFn: api.getDerivedStatus,
    refetchInterval: 30_000,
  });

  const resync = useMutation({
    mutationFn: api.resyncDerived,
    onSuccess: (s) => {
      qc.setQueryData(["derived-status"], s);
      // Aggregates were busted server-side; refresh the explorer too.
      qc.invalidateQueries({ queryKey: ["heartbeats"] });
      toast.success(
        s.inSync ? "Derived data resynced — in sync" : "Resynced (still drifting?)",
      );
    },
    onError: () => toast.error("Resync failed"),
  });

  const fmt = (n: number) => n.toLocaleString();
  const hrs = (s: number) => `${(s / 3600).toFixed(1)}h`;
  const bytes = (n: number) => {
    if (n < 1024) return `${n} B`;
    const u = ["KB", "MB", "GB", "TB"];
    let v = n / 1024;
    let i = 0;
    while (v >= 1024 && i < u.length - 1) {
      v /= 1024;
      i++;
    }
    return `${v.toFixed(1)} ${u[i]}`;
  };
  const inSync = data?.inSync ?? true;

  return (
    <div className="rounded-lg border bg-card p-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
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
      </div>

      {data && (
        <div className="mt-3 grid grid-cols-2 gap-4 sm:grid-cols-4 lg:grid-cols-6">
          <Cell label="Heartbeats" value={fmt(data.heartbeats)} />
          <Cell
            label="gap_seconds"
            value={`${fmt(data.gapPopulated)}${data.gapMissing > 1 ? ` (−${fmt(data.gapMissing)})` : ""}`}
          />
          <Cell label="Rollup rows" value={fmt(data.rollupRows)} />
          <Cell label="Rollup total" value={hrs(data.rollupSeconds)} />
          <Cell label="Raw total" value={hrs(data.rawSeconds)} />
          <Cell
            label="Drift"
            value={
              data.rollupSeconds === data.rawSeconds
                ? "0s"
                : `${fmt(Math.abs(data.rollupSeconds - data.rawSeconds))}s`
            }
          />
          <Cell label="heartbeats tbl" value={bytes(data.heartbeatsBytes)} />
          <Cell label="rollup tbl" value={bytes(data.rollupBytes)} />
          <Cell label="Database size" value={bytes(data.dbBytes)} />
        </div>
      )}
      {!inSync && !isLoading && (
        <p className="mt-2 text-xs text-amber-500/90">
          The rollup total differs from the raw heartbeats (or gaps are missing).
          Click Resync to rebuild the precomputed tables.
        </p>
      )}
    </div>
  );
}
