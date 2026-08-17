import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Card, CardContent } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { TableRowsSkeleton } from "@/components/Skeletons";
import { GroupByBar } from "@/features/explorer/GroupByBar";
import { ExplorerTable } from "@/features/explorer/ExplorerTable";
import type { LeafSort } from "@/features/explorer/useLeafSort";
import { useExplorerTree } from "@/features/explorer/useExplorerTree";
import type { DomainConfig } from "@/features/explorer/types";

interface GroupableExplorerProps<Row> {
  config: DomainConfig<Row>;
  // Ordered group-by axis ids (controlled).
  groupBy: string[];
  onGroupByChange: (next: string[]) => void;
  // Opaque reset token — folds the domain's query inputs (range/entity/…) so a
  // change drops every cache and reloads the root.
  resetKey: string;
  leafMode?: "table" | "json";
  // When true, the explorer does NOT render its own "Group by" bar — the caller
  // hosts <GroupByBar> itself (e.g. folded into a consolidated control bar). The
  // groupBy state stays controlled by the caller either way.
  hideGroupByBar?: boolean;
  // Optional controlled leaf-sort (e.g. persisted in the URL). Both must be set to
  // take control; omitted → the table owns its sort locally (default).
  sort?: LeafSort | null;
  onSortChange?: (s: LeafSort | null) => void;
}

/**
 * The whole public surface of the groupable explorer (gaka-02sh). Renders the
 * "Group by" bar + a server-driven drill-down table for any domain, driven
 * entirely by its DomainConfig. Zero group axes render leaf rows directly
 * unless the config supplies an `addAxisHint` (heartbeats requires an axis).
 */
export function GroupableExplorer<Row>({
  config,
  groupBy,
  onGroupByChange,
  resetKey,
  leafMode = "table",
  hideGroupByBar = false,
  sort,
  onSortChange,
}: GroupableExplorerProps<Row>) {
  const { labels } = config;
  const requireAxis = groupBy.length === 0 && labels.addAxisHint != null;
  const ctrl = useExplorerTree<Row>({
    config,
    axes: groupBy,
    resetKey,
    flatWhenEmpty: labels.addAxisHint == null,
  });

  return (
    <>
      {!hideGroupByBar && (
        <Card className="mb-4">
          <CardContent className="py-4">
            <GroupByBar
              axes={config.axes}
              groupBy={groupBy}
              onChange={onGroupByChange}
            />
          </CardContent>
        </Card>
      )}

      <Card>
        <CardContent className="py-3">
          {requireAxis ? (
            <p className="py-6 text-center text-sm text-muted-foreground">
              {labels.addAxisHint}
            </p>
          ) : ctrl.rootLoading ? (
            <TableRowsSkeleton rows={6} className="py-2" />
          ) : ctrl.rootError ? (
            <div className="space-y-2 py-6 text-center">
              <p className="text-sm text-destructive">
                {labels.loadError ?? "Failed to load groups."}
              </p>
              <Button
                variant="outline"
                size="sm"
                onClick={() => void ctrl.reloadRoot()}
              >
                Retry
              </Button>
            </div>
          ) : ctrl.tree.length === 0 ? (
            (labels.empty ?? null)
          ) : (
            <>
              {ctrl.rootTruncated && (
                <p className="mb-2 text-xs text-amber-500">
                  Showing the top groups only (results truncated).
                </p>
              )}
              <ExplorerTable
                ctrl={ctrl}
                config={config}
                leafMode={leafMode}
                sort={sort}
                onSortChange={onSortChange}
              />
            </>
          )}
        </CardContent>
      </Card>
    </>
  );
}
