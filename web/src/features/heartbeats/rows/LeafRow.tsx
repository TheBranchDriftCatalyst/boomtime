import { Fragment } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { JsonBlock } from "@/features/heartbeats/JsonBlock";
import { leafCellText } from "@/features/heartbeats/leafColumns";
import {
  INDENT,
  useExplorerRowContext,
} from "@/features/heartbeats/rows/explorerRowContext";
import { cn, truncate } from "@/lib/utils";
import type { LeafRowNode } from "@/features/heartbeats/explorerModel";

interface LeafRowProps {
  node: LeafRowNode;
  /** Whether the raw-JSON drawer is open. */
  expanded: boolean;
  onToggleExpanded: () => void;
}

/** A single heartbeat row (table mode) with an expandable raw-JSON drawer. */
export function LeafRow({ node: n, expanded, onToggleExpanded }: LeafRowProps) {
  const { visibleLeafColIds } = useExplorerRowContext();
  return (
    <Fragment>
      <tr className="border-t hover:bg-muted/30">
        <td className="px-2 py-1" style={{ paddingLeft: n.depth * INDENT + 8 }}>
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
        </td>
        {visibleLeafColIds.map((id) => (
          <td
            key={id}
            className={cn(
              "px-2 py-1 text-xs",
              id === "entity" && "max-w-[280px] truncate font-mono",
            )}
            title={id === "entity" ? n.row.entity : undefined}
          >
            {id === "entity"
              ? truncate(leafCellText(id, n.row), 48)
              : leafCellText(id, n.row)}
          </td>
        ))}
      </tr>
      {expanded && (
        <tr className="border-t bg-muted/10">
          <td colSpan={1 + visibleLeafColIds.length} className="p-2">
            <JsonBlock value={n.row} />
          </td>
        </tr>
      )}
    </Fragment>
  );
}
