import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/Spinner";
import { JsonBlock } from "@/components/heartbeats/JsonBlock";
import { LEAF_PAGE_SIZE } from "@/components/heartbeats/axes";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import type { HeartbeatFilters, HeartbeatRow } from "@/types/api";

interface HeartbeatLeafProps {
  start: string;
  end: string;
  filters: HeartbeatFilters;
  entity: string;
  mode: "table" | "json";
}

function fmtTime(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

function orNone(v: string | null): string {
  return v == null || v === "" ? "(none)" : v;
}

export function HeartbeatLeaf({
  start,
  end,
  filters,
  entity,
  mode,
}: HeartbeatLeafProps) {
  const [page, setPage] = useState(0);

  const query = useQuery({
    queryKey: ["heartbeats-list", start, end, filters, entity, page],
    queryFn: () =>
      api.listHeartbeats({
        start,
        end,
        filters,
        entity,
        page,
        limit: LEAF_PAGE_SIZE,
      }),
    placeholderData: (prev) => prev,
  });

  if (query.isLoading && !query.data) return <Spinner />;

  const data = query.data;
  const items = data?.items ?? [];
  const total = data?.total ?? 0;
  const limit = data?.limit ?? LEAF_PAGE_SIZE;
  const totalPages = Math.max(1, Math.ceil(total / limit));
  const currentPage = data?.page ?? page;

  return (
    <div className="space-y-3">
      {items.length === 0 ? (
        <p className="py-4 text-center text-sm text-muted-foreground">
          No heartbeats found.
        </p>
      ) : mode === "json" ? (
        <JsonBlock value={items} />
      ) : (
        <div className="overflow-hidden rounded-md border">
          <table className="w-full text-xs">
            <thead className="bg-muted/50 text-muted-foreground">
              <tr>
                <th className="w-6" />
                <th className="px-2 py-1.5 text-left font-medium">Time</th>
                <th className="px-2 py-1.5 text-left font-medium">Entity</th>
                <th className="px-2 py-1.5 text-left font-medium">Project</th>
                <th className="px-2 py-1.5 text-left font-medium">Language</th>
                <th className="px-2 py-1.5 text-left font-medium">Editor</th>
                <th className="px-2 py-1.5 text-left font-medium">Branch</th>
                <th className="px-2 py-1.5 text-left font-medium">Write</th>
              </tr>
            </thead>
            <tbody>
              {items.map((row) => (
                <LeafRow key={row.id} row={row} />
              ))}
            </tbody>
          </table>
        </div>
      )}

      {total > limit && (
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span>
            Page {currentPage + 1} of {totalPages} · {total.toLocaleString()}{" "}
            heartbeats
          </span>
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={currentPage <= 0}
              onClick={() => setPage((p) => Math.max(0, p - 1))}
            >
              Prev
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={currentPage >= totalPages - 1}
              onClick={() => setPage((p) => p + 1)}
            >
              Next
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}

function LeafRow({ row }: { row: HeartbeatRow }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <tr
        className={cn(
          "cursor-pointer border-t hover:bg-muted/40",
          open && "bg-muted/40",
        )}
        onClick={() => setOpen((o) => !o)}
      >
        <td className="pl-2 text-muted-foreground">
          {open ? (
            <ChevronDown className="h-3.5 w-3.5" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5" />
          )}
        </td>
        <td className="whitespace-nowrap px-2 py-1.5">{fmtTime(row.time)}</td>
        <td className="max-w-[220px] truncate px-2 py-1.5 font-mono">
          {row.entity}
        </td>
        <td className="px-2 py-1.5">{orNone(row.project)}</td>
        <td className="px-2 py-1.5">{orNone(row.language)}</td>
        <td className="px-2 py-1.5">{orNone(row.editor)}</td>
        <td className="px-2 py-1.5">{orNone(row.branch)}</td>
        <td className="px-2 py-1.5">
          {row.isWrite == null ? "-" : row.isWrite ? "yes" : "no"}
        </td>
      </tr>
      {open && (
        <tr className="border-t bg-muted/20">
          <td colSpan={8} className="p-2">
            <JsonBlock value={row} />
          </td>
        </tr>
      )}
    </>
  );
}
