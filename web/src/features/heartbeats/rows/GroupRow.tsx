import {
  Ban,
  ChevronDown,
  ChevronRight,
  Eye,
  EyeOff,
  Loader2,
  Pencil,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { axisLabel } from "@/lib/axes";
import {
  INDENT,
  useExplorerRowContext,
} from "@/features/heartbeats/rows/explorerRowContext";
import { cn, secondsToHms } from "@/lib/utils";
import type { GroupNode } from "@/features/heartbeats/explorerModel";
import type { ChildState } from "@/features/heartbeats/useExplorerTree";

function isRenamable(n: GroupNode): boolean {
  // Match the previous rule: non-null, and axis is not a synthetic/path axis.
  return n.value != null && n.axis !== "day" && n.axis !== "entity";
}

interface GroupRowProps {
  node: GroupNode;
  /** Lazy-load state for this node's children (loading/error/truncated). */
  state: ChildState | undefined;
  expanded: boolean;
  onToggle: () => void;
}

/** A drillable group row: value + count + duration, suppress/rename actions. */
export function GroupRow({ node: n, state, expanded, onToggle }: GroupRowProps) {
  const {
    getSuppressInfo,
    toggleSuppress,
    suppressBusy,
    getRenamedTo,
    onRename,
    visibleLeafColIds,
  } = useExplorerRowContext();

  const colSpan = 1 + visibleLeafColIds.length;
  const isNull = n.value == null;
  const suppress = getSuppressInfo(n);
  const isSuppressed = suppress.ruleId != null;
  const renamedTo = getRenamedTo(n);

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
              // Suppressed rows stay listed here (audit) but read as dimmed.
              isSuppressed && "opacity-50",
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
              {n.count.toLocaleString()}
            </Badge>
            <span className="shrink-0 text-xs text-muted-foreground">
              {secondsToHms(n.seconds)}
            </span>
            {isSuppressed && (
              <Badge
                variant="outline"
                className="shrink-0 border-amber-500/40 text-xs text-amber-500"
              >
                Hidden
              </Badge>
            )}
            {renamedTo != null && (
              <Badge
                variant="outline"
                className="shrink-0 border-violet-500/40 font-mono text-xs text-violet-400"
                title={`Remapped to "${renamedTo}" in your dashboards (reversible in Settings → Name remappings)`}
              >
                → {renamedTo}
              </Badge>
            )}
          </button>
          {suppress.suppressible && (
            <button
              className={cn(
                "rounded p-1 transition-opacity hover:bg-background hover:text-foreground disabled:opacity-40",
                // The active-suppressed toggle stays visible; the "suppress"
                // action reveals on hover/focus like the pencil.
                isSuppressed
                  ? "text-amber-500 opacity-100"
                  : "text-muted-foreground opacity-0 focus:opacity-100 group-hover/row:opacity-100",
              )}
              title={
                isSuppressed
                  ? `Unsuppress "${n.value}"`
                  : "Suppress (hide from dashboards)"
              }
              disabled={suppressBusy}
              onClick={(e) => {
                e.stopPropagation();
                toggleSuppress(n, suppress);
              }}
            >
              {isSuppressed ? (
                <Eye className="h-3.5 w-3.5" />
              ) : (
                <EyeOff className="h-3.5 w-3.5" />
              )}
            </button>
          )}
          {isRenamable(n) && (
            <button
              className="rounded p-1 text-muted-foreground opacity-0 transition-opacity hover:bg-background hover:text-foreground focus:opacity-100 group-hover/row:opacity-100"
              title={`Rename ${axisLabel(n.axis)} "${n.value}"`}
              onClick={(e) => {
                e.stopPropagation();
                onRename(n);
              }}
            >
              <Pencil className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
        {state?.error && (
          <p className="pl-6 text-xs text-destructive">
            Failed to load {n.nextAxis ? axisLabel(n.nextAxis) : "rows"}.
          </p>
        )}
        {state?.truncated && (
          <p className="pl-6 text-xs text-amber-500">
            Showing the top groups only (results truncated).
          </p>
        )}
      </td>
    </tr>
  );
}
