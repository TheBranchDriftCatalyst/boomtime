import { useEffect, useRef } from "react";
import { cn } from "@/lib/utils";

export type TerminalStatus = "idle" | "running" | "done" | "error";

interface TerminalLogViewerProps {
  output: string;
  status: TerminalStatus;
  /** Header label (e.g. the command or "output"). */
  title?: string;
  /** Small badge, e.g. "dry-run" / "applied". */
  statusLabel?: string;
  exitError?: string;
  durationMs?: number;
  truncated?: boolean;
  className?: string;
}

/** TerminalLogViewer (gaka-hney.5): a dark, monospace, auto-scrolling log/terminal
 * panel that streams appended output live. Presentational + reusable — the CLI
 * runner and the jobs UI (S2) both feed it. */
export function TerminalLogViewer({
  output,
  status,
  title = "output",
  statusLabel,
  exitError,
  durationMs,
  truncated,
  className,
}: TerminalLogViewerProps) {
  const scrollRef = useRef<HTMLDivElement>(null);

  // Stick to the bottom as new output streams in.
  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [output, status, exitError]);

  const dot =
    status === "running"
      ? "bg-amber-400 animate-pulse"
      : status === "error"
        ? "bg-destructive"
        : status === "done"
          ? "bg-emerald-400"
          : "bg-muted-foreground";

  return (
    <div
      className={cn(
        "overflow-hidden rounded-lg border border-border bg-[#0a0a12]",
        className,
      )}
      data-testid="terminal-log-viewer"
    >
      <div className="flex items-center gap-2 border-b border-border/60 bg-black/40 px-3 py-1.5 font-mono text-[11px] text-muted-foreground">
        <span className={cn("h-2 w-2 shrink-0 rounded-full", dot)} />
        <span className="truncate uppercase tracking-wide">{title}</span>
        {statusLabel && (
          <span className="rounded bg-muted px-1.5 text-foreground">{statusLabel}</span>
        )}
        <span className="ml-auto shrink-0">
          {status === "running" && "running…"}
          {status === "done" &&
            `done${durationMs != null ? ` · ${fmtMs(durationMs)}` : ""}`}
          {status === "error" &&
            `error${durationMs ? ` · ${fmtMs(durationMs)}` : ""}`}
        </span>
      </div>
      <div
        ref={scrollRef}
        className="max-h-[min(50vh,420px)] overflow-y-auto px-3 py-2"
      >
        <pre className="whitespace-pre-wrap break-words font-mono text-xs leading-relaxed text-foreground/90">
          {output}
          {status === "running" && (
            <span className="animate-pulse text-primary">▌</span>
          )}
          {truncated && "\n… [output truncated]"}
          {exitError && `\n\n✗ ${exitError}`}
        </pre>
      </div>
    </div>
  );
}

function fmtMs(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}
