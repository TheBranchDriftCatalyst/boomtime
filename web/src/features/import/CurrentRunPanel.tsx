import { useEffect, useState } from "react";
import { Ban, Wifi, WifiOff } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { ImportStateBadge } from "@/features/import/ImportStateBadge";
import { DriftBanner } from "@/features/import/DriftBanner";
import { LogTerminal } from "@/components/LogTerminal";
import { LabeledStat } from "@/components/LabeledStat";
import { formatElapsed } from "@/lib/utils";
import { isTerminalState } from "@/types/api";
import type { ImportJob, ImportLogLine } from "@/types/api";
import type { SocketStatus } from "@/features/import/useImportJobSocket";

interface CurrentRunPanelProps {
  job: ImportJob;
  logs: ImportLogLine[];
  status: SocketStatus;
  onCancel: () => void;
  cancelling: boolean;
}

export function CurrentRunPanel({
  job,
  logs,
  status,
  onCancel,
  cancelling,
}: CurrentRunPanelProps) {
  const terminal = isTerminalState(job.state);
  const pct =
    job.totalDays > 0
      ? Math.round((job.processedDays / job.totalDays) * 100)
      : job.state === "completed"
        ? 100
        : 0;

  // Tick once per second so the elapsed timer advances while running.
  const [, force] = useState(0);
  useEffect(() => {
    if (terminal) return;
    const id = window.setInterval(() => force((n) => n + 1), 1000);
    return () => window.clearInterval(id);
  }, [terminal]);

  const elapsed = formatElapsed(job.startedAt ?? job.createdAt, job.finishedAt);

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0">
        <div className="flex items-center gap-3">
          <CardTitle className="text-base">Current run #{job.id}</CardTitle>
          <ImportStateBadge state={job.state} />
          <ConnBadge status={status} terminal={terminal} />
        </div>
        {!terminal && (
          <Button
            variant="destructive"
            size="sm"
            onClick={onCancel}
            disabled={cancelling}
          >
            <Ban className="h-4 w-4" />
            {cancelling ? "Cancelling..." : "Cancel"}
          </Button>
        )}
      </CardHeader>
      <CardContent className="space-y-4">
        <div>
          <div className="mb-1.5 flex items-center justify-between text-sm">
            <span className="text-muted-foreground">
              {job.processedDays} / {job.totalDays} days
            </span>
            <span className="font-medium">{pct}%</span>
          </div>
          <Progress value={pct} />
        </div>

        <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
          <LabeledStat
            label="Imported"
            value={job.importedCount.toLocaleString()}
          />
          <LabeledStat label="Current day" value={job.currentDay ?? "-"} />
          <LabeledStat label="Elapsed" value={elapsed} />
          <LabeledStat
            label="Range"
            value={`${job.startDate.slice(0, 10)} → ${job.endDate.slice(0, 10)}`}
          />
        </div>

        {job.error && (
          <div className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
            {job.error}
          </div>
        )}

        <DriftBanner findings={job.drift} />

        <p className="text-xs text-muted-foreground">
          Re-running an import is safe: already-imported heartbeats are
          de-duplicated server-side, so no duplicates are created.
        </p>

        <LogTerminal logs={logs} />
      </CardContent>
    </Card>
  );
}

function ConnBadge({
  status,
  terminal,
}: {
  status: SocketStatus;
  terminal: boolean;
}) {
  if (terminal) return null;
  const connected = status === "open";
  return (
    <span
      className="flex items-center gap-1 text-xs text-muted-foreground"
      title={`Live stream: ${status}`}
    >
      {connected ? (
        <Wifi className="h-3.5 w-3.5 text-emerald-500" />
      ) : (
        <WifiOff className="h-3.5 w-3.5 text-amber-500" />
      )}
      {connected ? "Live" : "Reconnecting"}
    </span>
  );
}
