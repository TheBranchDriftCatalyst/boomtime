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
import { ColumnPicker } from "@shared/features/explorer/ColumnPicker";
import { GroupRow } from "@shared/features/explorer/rows/GroupRow";
import { LeafGroupRow } from "@shared/features/explorer/rows/LeafGroupRow";
import { LeafRow } from "@shared/features/explorer/rows/LeafRow";
import {
  ExplorerRowContext,
  defaultRenderJson,
  type ExplorerRowContextValue,
} from "@shared/features/explorer/rows/explorerRowContext";
import { useLeafSort, type LeafSort } from "@shared/features/explorer/useLeafSort";
import { ROOT_LEAF_ID } from "@shared/features/explorer/useExplorerTree";
import { cn } from "@shared/lib/utils";
import type { DomainConfig, GroupAction } from "@shared/features/explorer/types";
import type {
  ExplorerNode,
  GroupNode,
  LeafGroupNode,
} from "@shared/features/explorer/explorerModel";
import type { useExplorerTree } from "@shared/features/explorer/useExplorerTree";

type Tree = ReturnType<typeof useExplorerTree>;

// Stable no-op decorator for domains without a group decorator (keeps the hook
// call unconditional so React's hook order stays put across renders).
const NO_DECORATION = {};
const NOOP_DECORATE: GroupAction = () => NO_DECORATION;
function useNoopDecorator(): GroupAction {
  return NOOP_DECORATE;
}

interface Props<TRow> {
  ctrl: Tree;
  config: DomainConfig<TRow>;
  leafMode: "table" | "json";
  // Optional controlled sort (e.g. persisted in the URL). Omitted → local state.
  sort?: LeafSort | null;
  onSortChange?: (s: LeafSort | null) => void;
}

export function ExplorerTable<TRow>({ ctrl, config, leafMode, sort, onSortChange }: Props<TRow>) {
  // Seed the flat-root leaf group expanded so the zero-axis "Table" view shows
  // its rows (and the shared leaf pager) immediately. Inert when grouped — no
  // rendered row carries this id.
  const [expanded, setExpanded] = useState<ExpandedState>({
    [ROOT_LEAF_ID]: true,
  });
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>(
    () =>
      Object.fromEntries(
        config.columns.map((c) => [c.id, c.defaultVisible ?? true]),
      ) as VisibilityState,
  );

  // Sort each loaded leaf page client-side (server has no sort param). When the
  // host passes sort + onSortChange, run controlled (URL-persisted); else local.
  const { sorting, toggleSort, sortedTree } = useLeafSort(
    ctrl.tree,
    config.columns,
    sort !== undefined && onSortChange
      ? { sort, onSortChange }
      : undefined,
  );

  const columns = useMemo<ColumnDef<ExplorerNode>[]>(
    () => [
      // One synthetic column; cells render per node kind in the body below.
      { id: "tree", header: "" },
      ...config.columns.map(
        (c): ColumnDef<ExplorerNode> => ({ id: c.id, header: c.header }),
      ),
    ],
    [config.columns],
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

  // Domain-injected per-node decoration (badges/actions/dimming).
  const useDecorator = config.useGroupDecorator ?? useNoopDecorator;
  const decorate = useDecorator();

  const visibleCols = useMemo(
    () => config.columns.filter((c) => columnVisibility[c.id] !== false),
    [config.columns, columnVisibility],
  );

  const labelForAxis = useCallback(
    (id: string) => config.axes.find((a) => a.id === id)?.label ?? id,
    [config.axes],
  );

  const supportsJson = config.supportsJsonMode ?? false;
  const jsonMode = supportsJson && leafMode === "json";

  const rowContext = useMemo<ExplorerRowContextValue>(
    () => ({
      columns: visibleCols as unknown as ExplorerRowContextValue["columns"],
      rollups: config.rollups,
      leafGroupLabel: config.labels.leafGroup,
      supportsJson,
      jsonMode,
      renderJson: config.renderJson ?? defaultRenderJson,
      rowActions: config.rowActions as ExplorerRowContextValue["rowActions"],
      onRowSelect: config.onRowSelect as ExplorerRowContextValue["onRowSelect"],
      labelForAxis,
    }),
    [
      visibleCols,
      config.rollups,
      config.labels.leafGroup,
      config.renderJson,
      config.rowActions,
      config.onRowSelect,
      supportsJson,
      jsonMode,
      labelForAxis,
    ],
  );

  const treeHeader = config.labels.treeHeader ?? "Group / entity";

  return (
    <div>
      <div className="mb-2 flex items-center justify-end">
        <ColumnPicker
          columns={config.columns as unknown as ExplorerRowContextValue["columns"]}
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
              <th className="px-2 py-2 text-left font-medium">{treeHeader}</th>
              {visibleCols.map((c) => (
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
                      decoration={decorate(n, n.path)}
                      leafPage={ctrl.leafPages[n.id]}
                      onSetLeafPage={(page) => void ctrl.setLeafPage(n, page)}
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
                      onToggle={() => void toggleRow(row)}
                      onSetPage={(page) => void ctrl.setLeafPage(n, page)}
                    />
                  );
                }
                // leafRow — only render as columns in table mode (JSON mode
                // shows the array via the leafGroup above).
                if (jsonMode) return null;
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
