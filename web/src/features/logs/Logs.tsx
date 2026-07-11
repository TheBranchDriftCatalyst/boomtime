import { useLayoutEffect, useMemo, useRef, useState } from "react";
import { Trash2 } from "lucide-react";
import { PageToolbar } from "@/components/toolbar/PageToolbar";
import { Button } from "@/components/ui/button";
import { useLogsSocket, type SocketStatus } from "@/hooks/useLogsSocket";
import { cn } from "@/lib/utils";
import type { ServerLogEntry } from "@/types/api";

// Level filter options (ordered by severity). "all" shows everything.
const LEVELS = ["all", "debug", "info", "warn", "error"] as const;
type LevelFilter = (typeof LEVELS)[number];

function levelColor(level: string): string {
  switch (level.toLowerCase()) {
    case "error":
    case "fatal":
      return "text-red-400";
    case "warn":
    case "warning":
      return "text-amber-400";
    case "debug":
      return "text-slate-500";
    case "info":
      return "text-sky-400";
    default:
      return "text-slate-300";
  }
}

function normalizeLevel(level: string): LevelFilter | "other" {
  const l = level.toLowerCase();
  if (l === "warning") return "warn";
  if (l === "fatal") return "error";
  if (l === "debug" || l === "info" || l === "warn" || l === "error") return l;
  return "other";
}

function formatTs(ts: string): string {
  const d = new Date(ts);
  return Number.isNaN(d.getTime()) ? ts : d.toLocaleTimeString();
}

function formatAttrs(attrs?: Record<string, string> | null): string {
  if (!attrs) return "";
  return Object.entries(attrs)
    .map(([k, v]) => `${k}=${v}`)
    .join(" ");
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
export function Logs() {
  const { logs, status, clear } = useLogsSocket();
  const [filter, setFilter] = useState<LevelFilter>("all");

  const containerRef = useRef<HTMLDivElement>(null);
  const [pinned, setPinned] = useState(true);

  const visible = useMemo(() => {
    if (filter === "all") return logs;
    return logs.filter((l) => normalizeLevel(l.level) === filter);
  }, [logs, filter]);

  // Track whether the user is scrolled to (near) the bottom.
  function onScroll() {
    const el = containerRef.current;
    if (!el) return;
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
    setPinned(distance < 40);
  }

  // Auto-scroll to the bottom on new/visible logs while pinned.
  useLayoutEffect(() => {
    const el = containerRef.current;
    if (el && pinned) el.scrollTop = el.scrollHeight;
  }, [visible, pinned]);

  const st = statusStyles[status];

  return (
    <div>
      <PageToolbar title="Logs">
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
      </PageToolbar>

      {!pinned && (
        <div className="mb-2 flex justify-end">
          <Button
            variant="secondary"
            size="sm"
            onClick={() => setPinned(true)}
            title="Resume auto-scroll"
          >
            Jump to latest
          </Button>
        </div>
      )}

      <div
        ref={containerRef}
        onScroll={onScroll}
        className="h-[70vh] overflow-y-auto rounded-md border bg-slate-950 p-3 font-mono text-xs leading-relaxed"
      >
        {visible.length === 0 ? (
          <p className="text-slate-500">
            {logs.length === 0
              ? "Waiting for server logs..."
              : "No logs match this filter."}
          </p>
        ) : (
          visible.map((line: ServerLogEntry) => {
            const attrs = formatAttrs(line.attrs);
            return (
              <div
                key={line.id}
                className="whitespace-pre-wrap break-words"
              >
                <span className="text-slate-600">{formatTs(line.time)} </span>
                <span
                  className={cn(
                    "font-semibold uppercase",
                    levelColor(line.level),
                  )}
                >
                  [{line.level}]
                </span>{" "}
                <span className="text-slate-200">{line.msg}</span>
                {attrs && (
                  <span className="text-slate-500"> {attrs}</span>
                )}
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}
