import { useMemo, useState } from "react";
import { Trash2 } from "lucide-react";
import { PageToolbar } from "@/components/toolbar/PageToolbar";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { LogTerminal, type LogTerminalLine } from "@/components/LogTerminal";
import { useLogsSocket, type SocketStatus } from "@/features/logs/useLogsSocket";
import { cn } from "@/lib/utils";

// Level filter options (ordered by severity). "all" shows everything.
const LEVELS = ["all", "debug", "info", "warn", "error"] as const;
type LevelFilter = (typeof LEVELS)[number];

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
 * Logs — a live viewer of the running server process's own slog output,
 * streamed over WebSocket and durable across reloads (the server backfills its
 * ring buffer on (re)connect). Auto-scrolls to the newest line unless the user
 * scrolls up; supports a level filter and clearing the local buffer.
 */
export function Logs({ embedded = false }: { embedded?: boolean }) {
  const { logs, status, clear } = useLogsSocket();
  const [filter, setFilter] = useState<LevelFilter>("all");

  const visible = useMemo<LogTerminalLine[]>(() => {
    const matching =
      filter === "all"
        ? logs
        : logs.filter((l) => normalizeLevel(l.level) === filter);
    return matching.map((l) => ({
      id: l.id,
      ts: l.time,
      level: l.level,
      message: l.msg,
      attrs: l.attrs,
    }));
  }, [logs, filter]);

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

      <Button variant="outline" size="sm" onClick={clear} title="Clear the view">
        <Trash2 className="h-4 w-4" />
        Clear
      </Button>
    </>
  );

  return (
    <div>
      {embedded ? (
        <div className="mb-4 flex items-center justify-end gap-3">{controls}</div>
      ) : (
        <PageToolbar title="Logs">{controls}</PageToolbar>
      )}

      <LogTerminal
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
