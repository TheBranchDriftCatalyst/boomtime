import { useState } from "react";
import { Braces, Search, Table2 } from "lucide-react";
import { PageToolbar } from "@/components/toolbar/PageToolbar";
import { DateRangePicker } from "@/components/toolbar/DateRangePicker";
import { TimeLimitDropdown } from "@/components/toolbar/TimeLimitDropdown";
import { Spinner } from "@/components/Spinner";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { GroupByBar } from "@/features/heartbeats/GroupByBar";
import { DerivedStatusPanel } from "@/features/heartbeats/DerivedStatusPanel";
import { SourceHealthPanel } from "@/features/heartbeats/SourceHealthPanel";
import { HeartbeatExplorerTable } from "@/features/heartbeats/HeartbeatExplorerTable";
import { RenameGroupDialog } from "@/features/heartbeats/RenameGroupDialog";
import { useExplorerTree } from "@/features/heartbeats/useExplorerTree";
import { DEFAULT_GROUP_BY } from "@/features/heartbeats/axes";
import { useTimeRange } from "@/hooks/useTimeRange";
import type { HeartbeatAxis } from "@/types/api";
import type { GroupNode } from "@/features/heartbeats/explorerModel";

type LeafMode = "table" | "json";

export function Heartbeats() {
  const tr = useTimeRange();
  const [groupBy, setGroupBy] = useState<HeartbeatAxis[]>(DEFAULT_GROUP_BY);
  const [entity, setEntity] = useState("");
  const [entityInput, setEntityInput] = useState("");
  const [mode, setMode] = useState<LeafMode>("table");
  const [renameTarget, setRenameTarget] = useState<GroupNode | null>(null);

  // The explorer requires at least one group-by axis; the empty case renders a
  // hint below. The tree hook owns all server-driven expansion + pagination.
  const ctrl = useExplorerTree({
    axes: groupBy,
    start: tr.startISO,
    end: tr.endISO,
    timeLimit: tr.timeLimit,
    entity,
  });

  return (
    <div>
      <PageToolbar title="Heartbeats">
        <form
          className="relative"
          onSubmit={(e) => {
            e.preventDefault();
            setEntity(entityInput.trim());
          }}
        >
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={entityInput}
            onChange={(e) => setEntityInput(e.target.value)}
            onBlur={() => setEntity(entityInput.trim())}
            placeholder="Search entity..."
            className="h-8 w-52 pl-8"
          />
        </form>
        <div className="flex items-center rounded-md border p-0.5">
          <Button
            variant={mode === "table" ? "secondary" : "ghost"}
            size="sm"
            className="h-7"
            onClick={() => setMode("table")}
          >
            <Table2 className="h-4 w-4" />
            Table
          </Button>
          <Button
            variant={mode === "json" ? "secondary" : "ghost"}
            size="sm"
            className="h-7"
            onClick={() => setMode("json")}
          >
            <Braces className="h-4 w-4" />
            JSON
          </Button>
        </div>
        <TimeLimitDropdown value={tr.timeLimit} onChange={tr.setTimeLimit} />
        <DateRangePicker
          numDays={tr.numDays}
          onPreset={tr.setDaysFromToday}
          onRange={tr.setRange}
        />
      </PageToolbar>

      <div className="mb-4">
        <DerivedStatusPanel />
      </div>

      <div className="mb-4">
        <SourceHealthPanel />
      </div>

      <Card className="mb-4">
        <CardContent className="py-4">
          <GroupByBar groupBy={groupBy} onChange={setGroupBy} />
        </CardContent>
      </Card>

      <Card>
        <CardContent className="py-3">
          {groupBy.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted-foreground">
              Add at least one group-by axis to explore heartbeats.
            </p>
          ) : ctrl.rootLoading ? (
            <Spinner />
          ) : ctrl.rootError ? (
            <div className="space-y-2 py-6 text-center">
              <p className="text-sm text-destructive">
                Failed to load heartbeat groups.
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
            <p className="py-6 text-center text-sm text-muted-foreground">
              No heartbeats in this range.
            </p>
          ) : (
            <>
              {ctrl.rootTruncated && (
                <p className="mb-2 text-xs text-amber-500">
                  Showing the top groups only (results truncated).
                </p>
              )}
              <HeartbeatExplorerTable
                ctrl={ctrl}
                mode={mode}
                onRename={setRenameTarget}
              />
            </>
          )}
        </CardContent>
      </Card>

      <RenameGroupDialog
        node={renameTarget}
        onClose={() => setRenameTarget(null)}
      />
    </div>
  );
}
