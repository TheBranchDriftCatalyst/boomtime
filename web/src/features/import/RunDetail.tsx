import { useQuery } from "@tanstack/react-query";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { QueryGate } from "@/components/QueryGate";
import { ImportStateBadge } from "@/features/import/ImportStateBadge";
import { DriftBanner } from "@/features/import/DriftBanner";
import { LogTerminal } from "@/components/LogTerminal";
import { LabeledStat } from "@/components/LabeledStat";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { formatElapsed } from "@/lib/utils";

interface RunDetailProps {
  jobId: number;
  onClose: () => void;
}

/** Read-only view of a past run's job details and logs. */
export function RunDetail({ jobId, onClose }: RunDetailProps) {
  const query = useQuery({
    queryKey: qk.importJob(jobId),
    queryFn: () => api.getImportJob(jobId),
  });
  const data = query.data;

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0">
        <div className="flex items-center gap-3">
          <CardTitle className="text-base">Run #{jobId}</CardTitle>
          {data && <ImportStateBadge state={data.job.state} />}
        </div>
        <Button variant="ghost" size="icon" onClick={onClose}>
          <X className="h-4 w-4" />
        </Button>
      </CardHeader>
      <CardContent className="space-y-4">
        <QueryGate query={query} errorMessage="Failed to load import run.">
          {(data) => (
          <>
            <div className="grid grid-cols-2 gap-4 text-sm sm:grid-cols-4">
              <LabeledStat
                label="Range"
                value={`${data.job.startDate.slice(0, 10)} → ${data.job.endDate.slice(0, 10)}`}
              />
              <LabeledStat
                label="Imported"
                value={data.job.importedCount.toLocaleString()}
              />
              <LabeledStat
                label="Days"
                value={`${data.job.processedDays} / ${data.job.totalDays}`}
              />
              <LabeledStat
                label="Duration"
                value={formatElapsed(
                  data.job.startedAt ?? data.job.createdAt,
                  data.job.finishedAt,
                )}
              />
            </div>
            {data.job.error && (
              <div className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
                {data.job.error}
              </div>
            )}
            <DriftBanner findings={data.job.drift} />
            <LogTerminal logs={data.logs} />
          </>
          )}
        </QueryGate>
      </CardContent>
    </Card>
  );
}
