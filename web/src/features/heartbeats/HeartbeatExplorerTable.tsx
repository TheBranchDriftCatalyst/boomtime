import { useCallback, useMemo, useState } from "react";
import {
  getCoreRowModel,
  getExpandedRowModel,
  useReactTable,
  type ColumnDef,
  type ExpandedState,
  type Row,
  type VisibilityState,
} from "@tanstack/react-table";
import { ArrowUpDown } from "lucide-react";
import { ColumnPicker } from "@/features/heartbeats/ColumnPicker";
import { LEAF_COLUMNS } from "@/features/heartbeats/leafColumns";
import { GroupRow } from "@/features/heartbeats/rows/GroupRow";
import { LeafGroupRow } from "@/features/heartbeats/rows/LeafGroupRow";
import { LeafRow } from "@/features/heartbeats/rows/LeafRow";
import {
  ExplorerRowContext,
  type ExplorerRowContextValue,
} from "@/features/heartbeats/rows/explorerRowContext";
import { useLeafSort } from "@/features/heartbeats/useLeafSort";
import { useSuppression } from "@/features/heartbeats/useSuppression";
import { useSpaceMembership } from "@/features/heartbeats/useSpaceMembership";
import { cn } from "@/lib/utils";
import type {
  ExplorerNode,
  GroupNode,
  LeafGroupNode,
} from "@/features/heartbeats/explorerModel";
import type { useExplorerTree } from "@/features/heartbeats/useExplorerTree";

type Tree = ReturnType<typeof useExplorerTree>;

interface Props {
  ctrl: Tree;
  mode: "table" | "json";
  onRename: (node: GroupNode) => void;
}

export function HeartbeatExplorerTable({ ctrl, mode, onRename }: Props) {
  const [expanded, setExpanded] = useState<ExpandedState>({});
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>(
    () =>
      Object.fromEntries(
        LEAF_COLUMNS.map((c) => [c.id, c.defaultVisible]),
      ) as VisibilityState,
  );

  // Sort each loaded leaf page client-side (server has no sort param).
  const { sorting, toggleSort, sortedTree } = useLeafSort(ctrl.tree);

  const columns = useMemo<ColumnDef<ExplorerNode>[]>(
    () => [
      // One synthetic column; cells render per node kind in the body below.
      { id: "tree", header: "" },
      ...LEAF_COLUMNS.map(
        (c): ColumnDef<ExplorerNode> => ({ id: c.id, header: c.header }),
      ),
    ],
    [],
  );

  const getSubRows = useCallback((n: ExplorerNode) => {
    if (n.kind === "leafRow") return undefined;
    return (n as GroupNode | LeafGroupNode).subRows;
  }, []);

  const table = useReactTable<ExplorerNode>({
    data: sortedTree,
    columns,
    state: { expanded, columnVisibility },
    getRowId: (n) => n.id,
    getSubRows,
    onExpandedChange: setExpanded,
    onColumnVisibilityChange: setColumnVisibility,
    getCoreRowModel: getCoreRowModel(),
    getExpandedRowModel: getExpandedRowModel(),
    // Groups expand when drillable; leaf groups always; leaf rows toggle a JSON
    // drawer (expansion state, no subRows).
    getRowCanExpand: (row) => {
      const n = row.original;
      if (n.kind === "group") return n.drillable;
      return true; // leafGroup + leafRow
    },
  });

  const toggleRow = useCallback(
    async (row: Row<ExplorerNode>) => {
      const n = row.original;
      if (!row.getIsExpanded()) {
        await ctrl.ensureLoaded(n); // lazy-load children on first expand
      }
      row.toggleExpanded();
    },
    [ctrl],
  );

  // Suppress/rename actions shared by every group row (reversible curation
  // rules — the same set Settings manages).
  const { getSuppressInfo, getRenamedTo, toggleSuppress, suppressBusy } =
    useSuppression();

  // Space membership: badges for the Spaces a value already belongs to + an
  // "add to Space" action (both driven by exact Space membership rules).
  const { spaceOptions, getSpacesFor, canAddToSpace, addToSpace, spaceBusy } =
    useSpaceMembership();

  const visibleLeafCols = useMemo(
    () => LEAF_COLUMNS.filter((c) => columnVisibility[c.id] !== false),
    [columnVisibility],
  );

  const rowContext = useMemo<ExplorerRowContextValue>(
    () => ({
      getSuppressInfo,
      toggleSuppress,
      suppressBusy,
      getRenamedTo,
      onRename,
      getSpacesFor,
      canAddToSpace,
      spaceOptions,
      addToSpace,
      spaceBusy,
      visibleLeafColIds: visibleLeafCols.map((c) => c.id),
    }),
    [
      getSuppressInfo,
      toggleSuppress,
      suppressBusy,
      getRenamedTo,
      onRename,
      getSpacesFor,
      canAddToSpace,
      spaceOptions,
      addToSpace,
      spaceBusy,
      visibleLeafCols,
    ],
  );

  return (
    <div>
      <div className="mb-2 flex items-center justify-end">
        <ColumnPicker
          visibility={columnVisibility}
          onToggle={(id, v) =>
            setColumnVisibility((s) => ({ ...s, [id]: v }))
          }
        />
      </div>

      <div className="overflow-x-auto rounded-md border">
        <table className="w-full text-sm">
          <thead className="bg-muted/50 text-xs text-muted-foreground">
            <tr>
              <th className="px-2 py-2 text-left font-medium">Group / entity</th>
              {visibleLeafCols.map((c) => (
                <th
                  key={c.id}
                  className="cursor-pointer select-none px-2 py-2 text-left font-medium hover:text-foreground"
                  onClick={() => toggleSort(c.id)}
                >
                  <span className="inline-flex items-center gap-1">
                    {c.header}
                    <ArrowUpDown
                      className={cn(
                        "h-3 w-3",
                        sorting?.id === c.id ? "opacity-100" : "opacity-30",
                      )}
                    />
                  </span>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            <ExplorerRowContext.Provider value={rowContext}>
              {table.getRowModel().rows.map((row) => {
                const n = row.original;
                if (n.kind === "group") {
                  return (
                    <GroupRow
                      key={row.id}
                      node={n}
                      state={ctrl.childCache[n.id]}
                      expanded={row.getIsExpanded()}
                      onToggle={() => void toggleRow(row)}
                    />
                  );
                }
                if (n.kind === "leafGroup") {
                  return (
                    <LeafGroupRow
                      key={row.id}
                      node={n}
                      state={ctrl.childCache[n.id]}
                      page={ctrl.leafPages[n.id]}
                      expanded={row.getIsExpanded()}
                      mode={mode}
                      onToggle={() => void toggleRow(row)}
                      onSetPage={(page) => void ctrl.setLeafPage(n, page)}
                    />
                  );
                }
                // leafRow — only render as columns in table mode (JSON mode
                // shows the array via the leafGroup above).
                if (mode === "json") return null;
                return (
                  <LeafRow
                    key={row.id}
                    node={n}
                    expanded={row.getIsExpanded()}
                    onToggleExpanded={() => row.toggleExpanded()}
                  />
                );
              })}
            </ExplorerRowContext.Provider>
          </tbody>
        </table>
      </div>
    </div>
  );
}
