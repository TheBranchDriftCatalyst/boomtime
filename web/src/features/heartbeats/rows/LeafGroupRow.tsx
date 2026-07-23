import { ChevronDown, ChevronRight, Loader2 } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { JsonBlock } from "@/features/heartbeats/JsonBlock";
import {
  INDENT,
  useExplorerRowContext,
} from "@/features/heartbeats/rows/explorerRowContext";
import type { LeafGroupNode } from "@/features/heartbeats/explorerModel";
import type {
  ChildState,
  LeafPageState,
} from "@/features/heartbeats/useExplorerTree";

interface LeafGroupRowProps {
  node: LeafGroupNode;
  /** Lazy-load state for this leaf page (loading/error). */
  state: ChildState | undefined;
  /** Pagination state for this leaf group. */
  page: LeafPageState | undefined;
  expanded: boolean;
  mode: "table" | "json";
  onToggle: () => void;
  onSetPage: (page: number) => void;
}

/** The "Heartbeats" leaf-page row: expand toggle, pagination, JSON view. */
export function LeafGroupRow({
  node: n,
  state,
  page: pageState,
  expanded,
  mode,
  onToggle,
  onSetPage,
}: LeafGroupRowProps) {
  const { visibleLeafColIds } = useExplorerRowContext();
  const colSpan = 1 + visibleLeafColIds.length;

  const total = pageState?.total ?? 0;
  const limit = pageState?.limit ?? 50;
  const page = pageState?.page ?? 1;
  const totalPages = Math.max(1, Math.ceil(total / limit));

  return (
    <tr className="border-t bg-muted/20">
      <td colSpan={colSpan} className="px-2 py-1.5">
        <div
          className="flex flex-wrap items-center gap-3"
          style={{ paddingLeft: n.depth * INDENT }}
        >
          <button className="flex items-center gap-2 text-sm" onClick={onToggle}>
            <span className="flex h-4 w-4 items-center justify-center text-muted-foreground">
              {state?.loading ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : expanded ? (
                <ChevronDown className="h-4 w-4" />
              ) : (
                <ChevronRight className="h-4 w-4" />
              )}
            </span>
            <span className="font-medium">Heartbeats</span>
            {total > 0 && (
              <span className="text-xs text-muted-foreground">
                {total.toLocaleString()} rows
              </span>
            )}
          </button>
          {expanded && total > limit && (
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <span>
                Page {page} / {totalPages}
              </span>
              <Button
                variant="outline"
                size="sm"
                className="h-6"
                disabled={page <= 1 || state?.loading}
                onClick={() => onSetPage(page - 1)}
              >
                Prev
              </Button>
              <Button
                variant="outline"
                size="sm"
                className="h-6"
                disabled={page >= totalPages || state?.loading}
                onClick={() => onSetPage(page + 1)}
              >
                Next
              </Button>
            </div>
          )}
        </div>
        {expanded && mode === "json" && (
          <div className="mt-2" style={{ paddingLeft: n.depth * INDENT }}>
            <JsonBlock
              value={(n.subRows ?? []).map((r) =>
                r.kind === "leafRow" ? r.row : r,
              )}
            />
          </div>
        )}
      </td>
    </tr>
  );
}
