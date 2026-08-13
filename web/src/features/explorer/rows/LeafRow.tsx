import { Fragment } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import {
  INDENT,
  useExplorerRowContext,
} from "@/features/explorer/rows/explorerRowContext";
import { cn } from "@/lib/utils";
import type { LeafRowNode } from "@/features/explorer/explorerModel";

interface LeafRowProps {
  node: LeafRowNode;
  /** Whether the raw-JSON drawer is open. */
  expanded: boolean;
  onToggleExpanded: () => void;
}

/** A single domain row (table mode) with an optional expandable raw-JSON drawer. */
export function LeafRow({ node: n, expanded, onToggleExpanded }: LeafRowProps) {
  const { columns, supportsJson, renderJson, rowActions } =
    useExplorerRowContext();
  const row = n.row;
  return (
    <Fragment>
      <tr className="border-t hover:bg-muted/30">
        <td className="px-2 py-1" style={{ paddingLeft: n.depth * INDENT + 8 }}>
          <div className="flex items-center gap-2">
            {supportsJson && (
              <button
                className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
                onClick={onToggleExpanded}
                title="Show raw JSON"
              >
                {expanded ? (
                  <ChevronDown className="h-3.5 w-3.5" />
                ) : (
                  <ChevronRight className="h-3.5 w-3.5" />
                )}
                JSON
              </button>
            )}
            {rowActions?.(row)}
          </div>
        </td>
        {columns.map((col) => (
          <td
            key={col.id}
            className={cn("px-2 py-1 text-xs", col.cellClassName)}
            title={col.cellTitle?.(row)}
          >
            {col.render ? col.render(row) : String(col.get?.(row) ?? "")}
          </td>
        ))}
      </tr>
      {supportsJson && expanded && (
        <tr className="border-t bg-muted/10">
          <td colSpan={1 + columns.length} className="p-2">
            {renderJson(row)}
          </td>
        </tr>
      )}
    </Fragment>
  );
}
