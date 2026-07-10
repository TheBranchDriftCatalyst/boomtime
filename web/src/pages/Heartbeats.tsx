import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Braces, Search, Table2 } from "lucide-react";
import { PageToolbar } from "@/components/toolbar/PageToolbar";
import { DateRangePicker } from "@/components/toolbar/DateRangePicker";
import { Spinner } from "@/components/Spinner";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { GroupByBar } from "@/components/heartbeats/GroupByBar";
import { DerivedStatusPanel } from "@/components/heartbeats/DerivedStatusPanel";
import { HeartbeatGroupNode } from "@/components/heartbeats/HeartbeatGroupNode";
import { HeartbeatLeaf } from "@/components/heartbeats/HeartbeatLeaf";
import { DEFAULT_GROUP_BY } from "@/components/heartbeats/axes";
import { useTimeRange } from "@/hooks/useTimeRange";
import { api } from "@/lib/api";
import type { HeartbeatAxis } from "@/types/api";

type LeafMode = "table" | "json";

export function Heartbeats() {
  const tr = useTimeRange();
  const [groupBy, setGroupBy] = useState<HeartbeatAxis[]>(DEFAULT_GROUP_BY);
  const [entity, setEntity] = useState("");
  const [entityInput, setEntityInput] = useState("");
  const [mode, setMode] = useState<LeafMode>("table");

  // Root level: groups for the first axis (or the leaf directly if no axes).
  const rootAxis = groupBy[0];
  const rootQuery = useQuery({
    queryKey: ["heartbeats-group", rootAxis, {}, tr.startISO, tr.endISO],
    queryFn: () =>
      api.groupHeartbeats({
        groupBy: rootAxis,
        start: tr.startISO,
        end: tr.endISO,
      }),
    enabled: Boolean(rootAxis),
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
        <DateRangePicker
          numDays={tr.numDays}
          onPreset={tr.setDaysFromToday}
          onRange={tr.setRange}
        />
      </PageToolbar>

      <div className="mb-4">
        <DerivedStatusPanel />
      </div>

      <Card className="mb-4">
        <CardContent className="py-4">
          <GroupByBar groupBy={groupBy} onChange={setGroupBy} />
        </CardContent>
      </Card>

      <Card>
        <CardContent className="py-3">
          {!rootAxis ? (
            // No grouping: show the raw leaf directly.
            <HeartbeatLeaf
              start={tr.startISO}
              end={tr.endISO}
              filters={{}}
              entity={entity}
              mode={mode}
            />
          ) : rootQuery.isLoading ? (
            <Spinner />
          ) : rootQuery.isError ? (
            <p className="py-6 text-center text-sm text-destructive">
              Failed to load heartbeat groups.
            </p>
          ) : (rootQuery.data?.groups.length ?? 0) === 0 ? (
            <p className="py-6 text-center text-sm text-muted-foreground">
              No heartbeats in this range.
            </p>
          ) : (
            <div className="space-y-0.5">
              {rootQuery.data?.groups.map((group, i) => (
                <HeartbeatGroupNode
                  key={`${group.value ?? "__null__"}-${i}`}
                  group={group}
                  axis={rootAxis}
                  remainingAxes={groupBy.slice(1)}
                  parentFilters={{}}
                  start={tr.startISO}
                  end={tr.endISO}
                  entity={entity}
                  mode={mode}
                  depth={0}
                />
              ))}
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
