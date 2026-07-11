import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ImportStateBadge } from "@/features/import/ImportStateBadge";
import { cn, formatElapsed } from "@/lib/utils";
import type { ImportJob } from "@/types/api";

interface HistoryListProps {
  jobs: ImportJob[];
  selectedId: number | null;
  onSelect: (id: number) => void;
}

export function HistoryList({ jobs, selectedId, onSelect }: HistoryListProps) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Import history</CardTitle>
      </CardHeader>
      <CardContent>
        {jobs.length === 0 ? (
          <p className="py-6 text-center text-sm text-muted-foreground">
            No import runs yet.
          </p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>#</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Range</TableHead>
                <TableHead className="text-right">Imported</TableHead>
                <TableHead className="text-right">Duration</TableHead>
                <TableHead>Error</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {jobs.map((job) => (
                <TableRow
                  key={job.id}
                  onClick={() => onSelect(job.id)}
                  className={cn(
                    "cursor-pointer",
                    selectedId === job.id && "bg-muted",
                  )}
                >
                  <TableCell className="font-mono">{job.id}</TableCell>
                  <TableCell>
                    <ImportStateBadge state={job.state} />
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">
                    {job.startDate.slice(0, 10)} → {job.endDate.slice(0, 10)}
                  </TableCell>
                  <TableCell className="text-right font-mono">
                    {job.importedCount.toLocaleString()}
                  </TableCell>
                  <TableCell className="text-right font-mono text-muted-foreground">
                    {formatElapsed(
                      job.startedAt ?? job.createdAt,
                      job.finishedAt,
                    )}
                  </TableCell>
                  <TableCell className="max-w-[200px] truncate text-destructive">
                    {job.error ?? ""}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}
