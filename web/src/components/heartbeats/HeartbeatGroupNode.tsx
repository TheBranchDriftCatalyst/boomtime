import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, Ban, Pencil } from "lucide-react";
import { Spinner } from "@/components/Spinner";
import { HeartbeatLeaf } from "@/components/heartbeats/HeartbeatLeaf";
import { RenameGroupDialog } from "@/components/heartbeats/RenameGroupDialog";
import { axisLabel } from "@/components/heartbeats/axes";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import type {
  HeartbeatAxis,
  HeartbeatFilters,
  HeartbeatGroup,
} from "@/types/api";

interface HeartbeatGroupNodeProps {
  group: HeartbeatGroup;
  // The remaining axes to nest by, starting AFTER this group's axis.
  remainingAxes: HeartbeatAxis[];
  // The axis this group was produced by.
  axis: HeartbeatAxis;
  // Filters accumulated from ancestor groups (not yet including this group).
  parentFilters: HeartbeatFilters;
  start: string;
  end: string;
  entity: string;
  mode: "table" | "json";
  depth: number;
}

function fmtSpan(firstSeen: string, lastSeen: string): string {
  const f = new Date(firstSeen);
  const l = new Date(lastSeen);
  const fmt = (d: Date) =>
    Number.isNaN(d.getTime()) ? "?" : d.toLocaleDateString();
  const first = fmt(f);
  const last = fmt(l);
  return first === last ? first : `${first} → ${last}`;
}

/**
 * One expandable group row. On first expand it lazily loads either the next
 * axis's groups or (if no axes remain) the paginated leaf rows. TanStack Query
 * caches by (axis, filters), so re-expanding is instant.
 *
 * Null group values are shown but not drillable: passing a null down as a
 * filter is ambiguous against the backend's "absent = no filter" convention,
 * so we surface the group and stop there. Leaf loading is still allowed for a
 * null terminal group because no further filter is added.
 */
export function HeartbeatGroupNode({
  group,
  remainingAxes,
  axis,
  parentFilters,
  start,
  end,
  entity,
  mode,
  depth,
}: HeartbeatGroupNodeProps) {
  const [open, setOpen] = useState(false);

  const isNull = group.value == null;
  const nextAxis = remainingAxes[0];
  const isLeaf = !nextAxis;

  // Filters that apply to this group's children. Skip adding a null-valued
  // filter to avoid ambiguity (see the note above).
  const childFilters: HeartbeatFilters = isNull
    ? parentFilters
    : { ...parentFilters, [axis]: group.value as string };

  // A null non-leaf group cannot be drilled further (would need a null filter).
  const drillable = isLeaf || !isNull;

  const [renameOpen, setRenameOpen] = useState(false);
  // `day` buckets are synthetic and `entity` is a file path — neither is a
  // meaningful rename target. Null values have nothing to rename.
  const renamable = !isNull && axis !== "day" && axis !== "entity";

  const childrenQuery = useQuery({
    queryKey: [
      "heartbeats-group",
      nextAxis,
      childFilters,
      start,
      end,
    ],
    queryFn: () =>
      api.groupHeartbeats({
        groupBy: nextAxis,
        start,
        end,
        filters: childFilters,
      }),
    enabled: open && !isLeaf && drillable,
  });

  return (
    <div>
      <div
        className={cn(
          "group/row flex items-center gap-2 rounded-md pr-2 text-sm hover:bg-muted/50",
          !drillable && "hover:bg-transparent",
        )}
        // Structured for a future "edit/remap this group" action.
        data-axis={axis}
        data-value={group.value ?? ""}
      >
        <button
          className={cn(
            "flex min-w-0 flex-1 items-center gap-2 px-2 py-1.5 text-left",
            !drillable && "cursor-default",
          )}
          style={{ paddingLeft: `${depth * 16 + 8}px` }}
          onClick={() => drillable && setOpen((o) => !o)}
        >
          <span className="flex h-4 w-4 shrink-0 items-center justify-center text-muted-foreground">
            {!drillable ? (
              <Ban className="h-3.5 w-3.5" />
            ) : open ? (
              <ChevronDown className="h-4 w-4" />
            ) : (
              <ChevronRight className="h-4 w-4" />
            )}
          </span>
          <span
            className={cn(
              "truncate font-medium",
              isNull && "italic text-muted-foreground",
            )}
          >
            {isNull ? "(none)" : group.value}
          </span>
          <span className="shrink-0 rounded-full bg-secondary px-2 py-0.5 text-xs text-muted-foreground">
            {group.count.toLocaleString()}
          </span>
          <span className="ml-auto shrink-0 text-xs text-muted-foreground">
            {fmtSpan(group.firstSeen, group.lastSeen)}
          </span>
        </button>
        {renamable && (
          <button
            className="shrink-0 rounded p-1 text-muted-foreground opacity-0 transition-opacity hover:bg-background hover:text-foreground focus:opacity-100 group-hover/row:opacity-100"
            title={`Rename ${axisLabel(axis)} "${group.value}"`}
            onClick={(e) => {
              e.stopPropagation();
              setRenameOpen(true);
            }}
          >
            <Pencil className="h-3.5 w-3.5" />
          </button>
        )}
      </div>

      {renamable && (
        <RenameGroupDialog
          open={renameOpen}
          axis={axis}
          value={group.value as string}
          onClose={() => setRenameOpen(false)}
        />
      )}

      {open && drillable && (
        <div className="border-l" style={{ marginLeft: `${depth * 16 + 15}px` }}>
          {isLeaf ? (
            <div className="py-2 pl-3 pr-1">
              <HeartbeatLeaf
                start={start}
                end={end}
                filters={childFilters}
                entity={entity}
                mode={mode}
              />
            </div>
          ) : childrenQuery.isLoading ? (
            <Spinner />
          ) : childrenQuery.isError ? (
            <p className="py-2 pl-3 text-xs text-destructive">
              Failed to load {axisLabel(nextAxis)} groups.
            </p>
          ) : (childrenQuery.data?.groups.length ?? 0) === 0 ? (
            <p className="py-2 pl-3 text-xs text-muted-foreground">
              No {axisLabel(nextAxis)} groups.
            </p>
          ) : (
            childrenQuery.data?.groups.map((child, i) => (
              <HeartbeatGroupNode
                key={`${child.value ?? "__null__"}-${i}`}
                group={child}
                axis={nextAxis}
                remainingAxes={remainingAxes.slice(1)}
                parentFilters={childFilters}
                start={start}
                end={end}
                entity={entity}
                mode={mode}
                depth={depth + 1}
              />
            ))
          )}
        </div>
      )}
    </div>
  );
}
