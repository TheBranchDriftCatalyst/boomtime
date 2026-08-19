import { useMemo } from "react";
import { Link } from "react-router";
import { Activity } from "lucide-react";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { EmptyState } from "@shared/components/EmptyState";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import { AXES } from "@shared/lib/axes";
import { secondsToHms, truncate } from "@shared/lib/utils";
import { LEAF_COLUMNS, leafCellText } from "@boomtime/features/heartbeats/leafColumns";
import { LEAF_PAGE_SIZE } from "@boomtime/features/heartbeats/axes";
import { JsonBlock } from "@boomtime/features/heartbeats/JsonBlock";
import { curationLayer } from "@boomtime/features/curation/explorer/curationLayer";
import type {
  Axis,
  Column,
  DomainConfig,
  GroupPage,
  Rollup,
} from "@shared/features/explorer/types";
import type {
  HeartbeatAxis,
  HeartbeatGroupPayload,
  HeartbeatRow,
} from "@shared/types/api";

// Group axes offered by the heartbeats explorer (the shared AXES metadata).
const HEARTBEAT_AXES: Axis[] = AXES.map((a) => ({
  id: a.axis,
  label: a.label,
  section: a.section,
}));

// Leaf columns, mapping the heartbeats-specific cell text (+ the entity
// truncation/mono treatment) into the generic Column render slot.
const HEARTBEAT_COLUMNS: Column<HeartbeatRow>[] = LEAF_COLUMNS.map((c) => ({
  id: c.id,
  header: c.header,
  get: c.get,
  defaultVisible: c.defaultVisible,
  render:
    c.id === "entity"
      ? (r) => truncate(leafCellText("entity", r), 48)
      : (r) => leafCellText(c.id, r),
  cellClassName:
    c.id === "entity" ? "max-w-[280px] truncate font-mono" : undefined,
  cellTitle: c.id === "entity" ? (r) => r.entity : undefined,
}));

// The one heartbeats rollup shown on each group row (attributed coding time).
const HEARTBEAT_ROLLUPS: Rollup[] = [
  { id: "seconds", label: "Time", format: secondsToHms },
];

const EMPTY_STATE = (
  <EmptyState
    icon={Activity}
    title="No heartbeats in this range"
    description="Widen the date range, or import your history / set up a plugin to start streaming coding activity into the explorer."
    action={
      <Button asChild size="sm" variant="outline">
        <Link to="/app/import">Set up tracking</Link>
      </Button>
    }
  />
);

// The pluggable curation layer, composed in below. Built ONCE so the hook it
// returns keeps a stable identity across renders.
const useHeartbeatCuration = curationLayer();

// --- Drill path <-> heartbeat filter adapters (exported for tests) -----------

// Map a drill path to the legacy HeartbeatFilters shape, dropping any null
// step (the backend's absent = "no filter" convention).
export function pathToFilters(
  path: readonly { dim: string; value: string | null }[],
): Partial<Record<HeartbeatAxis, string>> {
  const filters: Partial<Record<HeartbeatAxis, string>> = {};
  for (const step of path) {
    if (step.value == null) continue;
    filters[step.dim as HeartbeatAxis] = step.value;
  }
  return filters;
}

// Map the legacy group payload to the generic GroupPage: count + seconds become
// per-group stats; firstSeen/lastSeen are dropped (unused by the UI).
export function toGroupPage(payload: HeartbeatGroupPayload): GroupPage {
  return {
    groups: payload.groups.map((g) => ({
      value: g.value,
      stats: { count: g.count, seconds: g.seconds },
    })),
    truncated: payload.truncated ?? false,
  };
}

// --- Config ------------------------------------------------------------------

interface Inputs {
  start: string;
  end: string;
  timeLimit: number;
  entity: string;
}

/**
 * The heartbeats DomainConfig for <GroupableExplorer>. Wraps the EXISTING
 * heartbeats group/list endpoints behind a TreeSource adapter (no backend
 * migration) and composes in the curation layer, JSON leaf mode, and copy.
 */
export function useHeartbeatsExplorerConfig({
  start,
  end,
  timeLimit,
  entity,
}: Inputs): DomainConfig<HeartbeatRow> {
  const qc = useQueryClient();

  const source = useMemo(
    () => ({
      fetchGroup: async (
        path: readonly { dim: string; value: string | null }[],
        axis: string,
      ): Promise<GroupPage> => {
        const filters = pathToFilters(path);
        const payload = await qc.fetchQuery({
          queryKey: qk.hbExploreGroup(
            axis as HeartbeatAxis,
            filters,
            start,
            end,
            timeLimit,
            entity,
          ),
          queryFn: () =>
            api.groupHeartbeats({
              groupBy: axis as HeartbeatAxis,
              start,
              end,
              timeLimit,
              filters,
              entity,
            }),
          staleTime: 30_000,
        });
        return toGroupPage(payload);
      },
      fetchLeaf: async (
        path: readonly { dim: string; value: string | null }[],
        page: number,
        pageSize: number,
      ) => {
        const filters = pathToFilters(path);
        const payload = await qc.fetchQuery({
          queryKey: qk.hbExploreList(filters, entity, start, end, page),
          queryFn: () =>
            api.listHeartbeats({
              start,
              end,
              filters,
              entity,
              page,
              limit: pageSize,
            }),
          staleTime: 30_000,
        });
        return {
          rows: payload.items,
          total: payload.total,
          page: payload.page,
          limit: payload.limit,
        };
      },
    }),
    [qc, start, end, timeLimit, entity],
  );

  return useMemo<DomainConfig<HeartbeatRow>>(
    () => ({
      axes: HEARTBEAT_AXES,
      defaultGroupBy: ["project", "day"],
      columns: HEARTBEAT_COLUMNS,
      rollups: HEARTBEAT_ROLLUPS,
      source,
      rowKey: (r) => String(r.id),
      leafPageSize: LEAF_PAGE_SIZE,
      labels: {
        leafGroup: "Heartbeats",
        treeHeader: "Group / entity",
        addAxisHint: "Add at least one group-by axis to explore heartbeats.",
        loadError: "Failed to load heartbeat groups.",
        empty: EMPTY_STATE,
      },
      supportsJsonMode: true,
      renderJson: (value) => <JsonBlock value={value} />,
      // Compose in the pluggable curation layer (suppress/rename/add-to-Space).
      useGroupDecorator: useHeartbeatCuration,
    }),
    [source],
  );
}
