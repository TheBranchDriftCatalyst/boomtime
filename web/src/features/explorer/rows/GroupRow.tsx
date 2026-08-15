import {
  Ban,
  ChevronDown,
  ChevronRight,
  Loader2,
} from "lucide-react";
import { Badge } from "@thebranchdriftcatalyst/catalyst-ui/ui/badge";
import {
  INDENT,
  useExplorerRowContext,
} from "@/features/explorer/rows/explorerRowContext";
import { LeafPager } from "@/features/explorer/rows/LeafGroupRow";
import { cn } from "@/lib/utils";
import type { GroupDecoration } from "@/features/explorer/types";
import type { GroupNode, LeafRowNode } from "@/features/explorer/explorerModel";
import type {
  ChildState,
  LeafPageState,
} from "@/features/explorer/useExplorerTree";

interface GroupRowProps {
  node: GroupNode;
  /** Lazy-load state for this node's children (loading/error/truncated). */
  state: ChildState | undefined;
  expanded: boolean;
  onToggle: () => void;
  /** Domain-injected badges/actions/dimming for this node. */
  decoration?: GroupDecoration;
  /**
   * Leaf pagination for a TERMINAL group (no nextAxis): it owns its leaf rows
   * directly, so the pager (and the JSON blob in JSON mode) render inline under
   * this row instead of on a separate labeled leaf-group node.
   */
  leafPage?: LeafPageState;
  onSetLeafPage?: (page: number) => void;
}

/**
 * A drillable group row: value + count + rollups. Domain decorations (badges +
 * actions + dimming) are injected via `decoration` so this generic row stays
 * domain-agnostic.
 */
export function GroupRow({
  node: n,
  state,
  expanded,
  onToggle,
  decoration,
  leafPage,
  onSetLeafPage,
}: GroupRowProps) {
  const { columns, rollups, labelForAxis, jsonMode, renderJson } =
    useExplorerRowContext();

  const colSpan = 1 + columns.length;
  const isNull = n.value == null;
  // A terminal group (deepest axis) owns its paginated leaf rows directly.
  const isTerminal = !n.nextAxis;
  const leafIndent = (n.depth + 1) * INDENT;

  return (
    <tr className="group/row border-t hover:bg-muted/40">
      <td colSpan={colSpan} className="px-2 py-1.5">
        <div
          className="flex items-center gap-2"
          style={{ paddingLeft: n.depth * INDENT }}
        >
          <button
            className={cn(
              "flex flex-1 items-center gap-2 text-left",
              !n.drillable && "cursor-default",
              // Dimmed rows stay listed here (audit) but read as muted.
              decoration?.dimmed && "opacity-50",
            )}
            onClick={() => n.drillable && onToggle()}
          >
            <span className="flex h-4 w-4 items-center justify-center text-muted-foreground">
              {!n.drillable ? (
                <Ban className="h-3.5 w-3.5" />
              ) : state?.loading ? (
                <Loader2 className="h-4 w-4 animate-spin" />
              ) : expanded ? (
                <ChevronDown className="h-4 w-4" />
              ) : (
                <ChevronRight className="h-4 w-4" />
              )}
            </span>
            <span
              className={cn(
                "font-medium",
                isNull && "italic text-muted-foreground",
              )}
            >
              {isNull ? "(none)" : n.value}
            </span>
            <Badge variant="secondary" className="shrink-0 font-mono text-xs">
              {n.stats.count.toLocaleString()}
            </Badge>
            {rollups.map((r) => (
              <span
                key={r.id}
                className="shrink-0 text-xs text-muted-foreground"
              >
                {r.format
                  ? r.format(n.stats[r.id] ?? 0)
                  : (n.stats[r.id] ?? 0).toLocaleString()}
              </span>
            ))}
            {decoration?.badges}
          </button>
          {decoration?.actions}
        </div>
        {state?.error && (
          <p className="pl-6 text-xs text-destructive">
            Failed to load {n.nextAxis ? labelForAxis(n.nextAxis) : "rows"}.
          </p>
        )}
        {state?.truncated && (
          <p className="pl-6 text-xs text-amber-500">
            Showing the top groups only (results truncated).
          </p>
        )}
        {isTerminal && expanded && onSetLeafPage && (
          <LeafPager
            className="mt-1.5"
            style={{ paddingLeft: leafIndent }}
            page={leafPage}
            onSetPage={onSetLeafPage}
            loading={state?.loading}
          />
        )}
        {isTerminal && expanded && jsonMode && (
          <div className="mt-2" style={{ paddingLeft: leafIndent }}>
            {renderJson(
              (n.subRows ?? []).map((r) =>
                r.kind === "leafRow" ? (r as LeafRowNode).row : r,
              ),
            )}
          </div>
        )}
      </td>
    </tr>
  );
}
