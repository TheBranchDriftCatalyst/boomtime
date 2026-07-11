import { useQuery } from "@tanstack/react-query";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Spinner } from "@/components/Spinner";
import { ImportStateBadge } from "@/components/import/ImportStateBadge";
import { LogTerminal } from "@/components/import/LogTerminal";
import { api } from "@/lib/api";
import { formatElapsed } from "@/lib/utils";

interface RunDetailProps {
  jobId: number;
  onClose: () => void;
}

/** Read-only view of a past run's job details and logs. */
export function RunDetail({ jobId, onClose }: RunDetailProps) {
  const { data, isLoading } = useQuery({
    queryKey: ["import-job", jobId],
    queryFn: () => api.getImportJob(jobId),
  });

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
        {isLoading || !data ? (
          <Spinner />
        ) : (
          <>
            <div className="grid grid-cols-2 gap-4 text-sm sm:grid-cols-4">
              <Field
                label="Range"
                value={`${data.job.startDate.slice(0, 10)} → ${data.job.endDate.slice(0, 10)}`}
              />
              <Field
                label="Imported"
                value={data.job.importedCount.toLocaleString()}
              />
              <Field
                label="Days"
                value={`${data.job.processedDays} / ${data.job.totalDays}`}
              />
              <Field
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
            <LogTerminal logs={data.logs} />
          </>
        )}
      </CardContent>
    </Card>
  );
}

function Field({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <p className="text-xs uppercase tracking-wide text-muted-foreground">
        {label}
      </p>
      <p className="mt-0.5 font-mono font-medium">{value}</p>
    </div>
  );
}
