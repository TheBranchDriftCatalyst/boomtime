import { Badge } from "@thebranchdriftcatalyst/catalyst-ui/ui/badge";
import { cn } from "@/lib/utils";
import type { ImportJobState } from "@/types/api";

const STYLES: Record<ImportJobState, string> = {
  queued: "bg-slate-500/15 text-slate-500",
  running: "bg-sky-500/15 text-sky-500",
  completed: "bg-emerald-500/15 text-emerald-500",
  failed: "bg-red-500/15 text-red-500",
  cancelled: "bg-amber-500/15 text-amber-500",
};

const LABELS: Record<ImportJobState, string> = {
  queued: "Queued",
  running: "Running",
  completed: "Completed",
  failed: "Failed",
  cancelled: "Cancelled",
};

export function ImportStateBadge({ state }: { state: ImportJobState }) {
  return (
    <Badge
      variant="outline"
      className={cn("border-transparent", STYLES[state])}
    >
      {state === "running" && (
        <span className="mr-1.5 inline-block h-1.5 w-1.5 animate-pulse rounded-full bg-current" />
      )}
      {LABELS[state]}
    </Badge>
  );
}
