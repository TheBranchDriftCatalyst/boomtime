import { useMemo, useState } from "react";
import { Trash2 } from "lucide-react";
import { PageToolbar } from "@thebranchdriftcatalyst/catalyst-ui/components/PageToolbar";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { LogViewer, type LogViewerLine } from "@thebranchdriftcatalyst/catalyst-ui/components/LogViewer";
import { useLogsSocket, type SocketStatus } from "@shared/features/logs/useLogsSocket";
import { toLogViewerLine } from "@shared/features/logs/logLine";
import { cn } from "@shared/lib/utils";

// Level filter options (ordered by severity). "all" shows everything.
const LEVELS = ["all", "debug", "info", "warn", "error"] as const;
type LevelFilter = (typeof LEVELS)[number];

// Source filter: server's own logs vs. records relayed from a
// boomtime-worker pod over the cross-pod log relay (Redis pub/sub -> the
// server's LogHub). "all" shows both — see internal/logging/redis_relay.go.
const SOURCES = ["all", "server", "worker"] as const;
type SourceFilter = (typeof SOURCES)[number];

function normalizeLevel(level: string): LevelFilter | "other" {
  const l = level.toLowerCase();
  if (l === "warning") return "warn";
  if (l === "fatal") return "error";
  if (l === "debug" || l === "info" || l === "warn" || l === "error") return l;
  return "other";
}

const statusStyles: Record<SocketStatus, { label: string; dot: string }> = {
  connecting: { label: "Connecting", dot: "bg-amber-400" },
  open: { label: "Live", dot: "bg-emerald-400 animate-pulse" },
  reconnecting: { label: "Reconnecting", dot: "bg-amber-400 animate-pulse" },
  closed: { label: "Disconnected", dot: "bg-slate-500" },
};

/**
 * Logs — a live viewer of the server process's slog output, streamed over
 * WebSocket and durable across reloads (the server backfills its ring
 * buffer on (re)connect). Since the worker-topology cutover this also
 * includes the separate boomtime-worker pod's logs, relayed into the
 * server's hub over Redis pub/sub (see internal/logging/redis_relay.go) and
 * tagged with a "source" (server|worker) — the source filter below lets you
 * isolate either. Auto-scrolls to the newest line unless the user scrolls
 * up; supports level + source filters and clearing the local buffer.
 */
export function Logs({ embedded = false }: { embedded?: boolean }) {
  const { logs, status, clear } = useLogsSocket();
  const [filter, setFilter] = useState<LevelFilter>("all");
  const [sourceFilter, setSourceFilter] = useState<SourceFilter>("all");

  const visible = useMemo<LogViewerLine[]>(() => {
    const matching = logs.filter(
      (l) =>
        (filter === "all" || normalizeLevel(l.level) === filter) &&
        (sourceFilter === "all" || l.source === sourceFilter),
    );
    // Shared mapping with the per-job panel: folds source (+ host) into the
    // dim attrs tail. We keep every attr here — including `job_id` — so a
    // job-tagged line surfaces its id inline in the full viewer (gaka-f0is);
    // the per-job panel omits job_id/kind/owner since its header implies them.
    return matching.map((l) => toLogViewerLine(l));
  }, [logs, filter, sourceFilter]);

  const st = statusStyles[status];

  const controls = (
    <>
      <span className="flex items-center gap-1.5 text-sm text-muted-foreground">
        <span className={cn("h-2 w-2 rounded-full", st.dot)} />
        {st.label}
      </span>

      <div className="flex items-center gap-1 rounded-md border p-0.5">
        {LEVELS.map((lvl) => (
          <button
            key={lvl}
            onClick={() => setFilter(lvl)}
            className={cn(
              "rounded px-2 py-1 text-xs font-medium capitalize transition-colors",
              filter === lvl
                ? "bg-primary text-primary-foreground"
                : "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
            )}
          >
            {lvl}
          </button>
        ))}
      </div>

      <div className="flex items-center gap-1 rounded-md border p-0.5">
        {SOURCES.map((src) => (
          <button
            key={src}
            onClick={() => setSourceFilter(src)}
            className={cn(
              "rounded px-2 py-1 text-xs font-medium capitalize transition-colors",
              sourceFilter === src
                ? "bg-primary text-primary-foreground"
                : "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
            )}
          >
            {src}
          </button>
        ))}
      </div>

      <Button variant="outline" size="sm" onClick={clear} title="Clear the view">
        <Trash2 className="h-4 w-4" />
        Clear
      </Button>
    </>
  );

  return (
    <div>
      {embedded ? (
        <div className="mb-4 flex flex-wrap items-center justify-end gap-3">{controls}</div>
      ) : (
        <PageToolbar title="Logs">{controls}</PageToolbar>
      )}

      <LogViewer
        logs={visible}
        height="h-[70vh]"
        emptyText={
          logs.length === 0
            ? "Waiting for server logs..."
            : "No logs match this filter."
        }
      />
    </div>
  );
}
