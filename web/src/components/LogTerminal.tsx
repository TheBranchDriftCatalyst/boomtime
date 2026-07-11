import { useLayoutEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/**
 * The line shape LogTerminal renders. ImportLogLine matches it structurally;
 * other producers (e.g. the server-log stream) map their entries onto it.
 */
export interface LogTerminalLine {
  id: number;
  ts: string;
  level: string;
  message: string;
  /** Optional structured attributes, rendered dimmed as `k=v` pairs. */
  attrs?: Record<string, string> | null;
}

interface LogTerminalProps {
  logs: LogTerminalLine[];
  className?: string;
  /** Tailwind height class for the scroll area. Default: "h-80". */
  height?: string;
  /** Shown when there are no lines. */
  emptyText?: string;
}

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

/**
 * Monospace terminal that auto-scrolls to the bottom unless the user scrolls
 * up; while unpinned a floating "Jump to latest" button resumes auto-scroll.
 */
export function LogTerminal({
  logs,
  className,
  height = "h-80",
  emptyText = "Waiting for logs...",
}: LogTerminalProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [pinned, setPinned] = useState(true);

  // Track whether the user is scrolled to (near) the bottom.
  function onScroll() {
    const el = containerRef.current;
    if (!el) return;
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight;
    setPinned(distance < 40);
  }

  // Auto-scroll to the bottom on new logs (and on mount) while pinned.
  useLayoutEffect(() => {
    const el = containerRef.current;
    if (el && pinned) el.scrollTop = el.scrollHeight;
  }, [logs, pinned]);

  return (
    <div className="relative">
      <div
        ref={containerRef}
        onScroll={onScroll}
        className={cn(
          "overflow-y-auto rounded-md border bg-slate-950 p-3 font-mono text-xs leading-relaxed",
          height,
          className,
        )}
      >
        {logs.length === 0 ? (
          <p className="text-slate-500">{emptyText}</p>
        ) : (
          logs.map((line) => {
            const attrs = formatAttrs(line.attrs);
            return (
              <div key={line.id} className="whitespace-pre-wrap break-words">
                <span className="text-slate-600">{formatTs(line.ts)} </span>
                <span
                  className={cn(
                    "font-semibold uppercase",
                    levelColor(line.level),
                  )}
                >
                  [{line.level}]
                </span>{" "}
                <span className="text-slate-200">{line.message}</span>
                {attrs && <span className="text-slate-500"> {attrs}</span>}
              </div>
            );
          })
        )}
      </div>
      {!pinned && (
        <div className="absolute bottom-3 right-3">
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
    </div>
  );
}
