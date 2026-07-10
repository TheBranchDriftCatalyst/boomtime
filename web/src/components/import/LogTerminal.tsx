import { useLayoutEffect, useRef, useState } from "react";
import { cn } from "@/lib/utils";
import type { ImportLogLine } from "@/types/api";

interface LogTerminalProps {
  logs: ImportLogLine[];
  className?: string;
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

/** Monospace terminal that auto-scrolls to the bottom unless the user scrolls up. */
export function LogTerminal({ logs, className }: LogTerminalProps) {
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
    <div
      ref={containerRef}
      onScroll={onScroll}
      className={cn(
        "h-80 overflow-y-auto rounded-md border bg-slate-950 p-3 font-mono text-xs leading-relaxed",
        className,
      )}
    >
      {logs.length === 0 ? (
        <p className="text-slate-500">Waiting for logs...</p>
      ) : (
        logs.map((line) => (
          <div key={line.id} className="whitespace-pre-wrap break-words">
            <span className="text-slate-600">{formatTs(line.ts)} </span>
            <span
              className={cn("font-semibold uppercase", levelColor(line.level))}
            >
              [{line.level}]
            </span>{" "}
            <span className="text-slate-200">{line.message}</span>
          </div>
        ))
      )}
    </div>
  );
}
