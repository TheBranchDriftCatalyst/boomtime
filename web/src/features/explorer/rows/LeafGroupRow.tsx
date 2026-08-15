import { ChevronDown, ChevronRight, Loader2 } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  INDENT,
  useExplorerRowContext,
} from "@/features/explorer/rows/explorerRowContext";
import { cn } from "@/lib/utils";
import type { LeafGroupNode, LeafRowNode } from "@/features/explorer/explorerModel";
import type {
  ChildState,
  LeafPageState,
} from "@/features/explorer/useExplorerTree";

interface LeafPagerProps {
  /** Pagination state for the leaf owner (undefined => not yet loaded). */
  page: LeafPageState | undefined;
  onSetPage: (page: number) => void;
  loading?: boolean;
  className?: string;
  style?: React.CSSProperties;
}

/**
 * Prev / page-of / Next pager for a paginated leaf owner. Renders nothing when
 * everything fits on one page. Shared by the flat-root leaf-group (inline with
 * its label) and each terminal group (inline under its own row).
 */
export function LeafPager({
  page: pageState,
  onSetPage,
  loading,
  className,
  style,
}: LeafPagerProps) {
  const total = pageState?.total ?? 0;
  const limit = pageState?.limit ?? 50;
  const page = pageState?.page ?? 1;
  const totalPages = Math.max(1, Math.ceil(total / limit));
  if (total <= limit) return null;

  return (
    <div
      className={cn(
        "flex items-center gap-2 text-xs text-muted-foreground",
        className,
      )}
      style={style}
    >
      <span>
        Page {page} / {totalPages}
      </span>
      <Button
        variant="outline"
        size="sm"
        className="h-6"
        disabled={page <= 1 || loading}
        onClick={() => onSetPage(page - 1)}
      >
        Prev
      </Button>
      <Button
        variant="outline"
        size="sm"
        className="h-6"
        disabled={page >= totalPages || loading}
        onClick={() => onSetPage(page + 1)}
      >
        Next
      </Button>
    </div>
  );
}

interface LeafGroupRowProps {
  node: LeafGroupNode;
  /** Lazy-load state for this leaf page (loading/error). */
  state: ChildState | undefined;
  /** Pagination state for this leaf group. */
  page: LeafPageState | undefined;
  expanded: boolean;
  onToggle: () => void;
  onSetPage: (page: number) => void;
}

/** The leaf-page row: expand toggle, pagination, optional JSON view. */
export function LeafGroupRow({
  node: n,
  state,
  page: pageState,
  expanded,
  onToggle,
  onSetPage,
}: LeafGroupRowProps) {
  const { columns, leafGroupLabel, jsonMode, renderJson } =
    useExplorerRowContext();
  const colSpan = 1 + columns.length;

  const total = pageState?.total ?? 0;

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
            <span className="font-medium">{leafGroupLabel}</span>
            {total > 0 && (
              <span className="text-xs text-muted-foreground">
                {total.toLocaleString()} rows
              </span>
            )}
          </button>
          {expanded && (
            <LeafPager
              page={pageState}
              onSetPage={onSetPage}
              loading={state?.loading}
            />
          )}
        </div>
        {expanded && jsonMode && (
          <div className="mt-2" style={{ paddingLeft: n.depth * INDENT }}>
            {renderJson(
              (n.subRows ?? []).map((r: LeafRowNode) =>
                r.kind === "leafRow" ? r.row : r,
              ),
            )}
          </div>
        )}
      </td>
    </tr>
  );
}
