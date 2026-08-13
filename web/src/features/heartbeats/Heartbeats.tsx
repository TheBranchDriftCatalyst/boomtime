import { useState } from "react";
import { Braces, Files, Search, Table2 } from "lucide-react";
import { Page } from "@/layout/Page";
import { DateRangePicker } from "@/components/toolbar/DateRangePicker";
import { TimeLimitDropdown } from "@/components/toolbar/TimeLimitDropdown";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { BackupPanel } from "@/features/heartbeats/BackupPanel";
import { SourceHealthPanel } from "@/features/heartbeats/SourceHealthPanel";
import { EntityExplorer } from "@/features/heartbeats/EntityExplorer";
import { GroupableExplorer } from "@/features/explorer/GroupableExplorer";
import { useHeartbeatsExplorerConfig } from "@/features/heartbeats/explorerConfig";
import { DEFAULT_GROUP_BY } from "@/features/heartbeats/axes";
import { useTimeRange } from "@/hooks/useTimeRange";

type LeafMode = "table" | "json";
type Tab = "explorer" | "entities";

export function Heartbeats() {
  const tr = useTimeRange();
  const [tab, setTab] = useState<Tab>("explorer");
  const [groupBy, setGroupBy] = useState<string[]>(DEFAULT_GROUP_BY);
  const [entity, setEntity] = useState("");
  const [entityInput, setEntityInput] = useState("");
  const [mode, setMode] = useState<LeafMode>("table");

  // The heartbeats DomainConfig wraps the existing group/list endpoints; the
  // shared <GroupableExplorer> owns all server-driven expansion + pagination.
  const config = useHeartbeatsExplorerConfig({
    start: tr.startISO,
    end: tr.endISO,
    timeLimit: tr.timeLimit,
    entity,
  });

  return (
    <Page>
      <Page.Header title="Heartbeats">
        <div className="flex items-center rounded-md border p-0.5">
          <Button
            variant={tab === "explorer" ? "secondary" : "ghost"}
            size="sm"
            className="h-7"
            onClick={() => setTab("explorer")}
          >
            <Search className="h-4 w-4" />
            Explorer
          </Button>
          <Button
            variant={tab === "entities" ? "secondary" : "ghost"}
            size="sm"
            className="h-7"
            onClick={() => setTab("entities")}
          >
            <Files className="h-4 w-4" />
            Entities
          </Button>
        </div>
        {tab === "explorer" && (
          <>
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
            <TimeLimitDropdown
              value={tr.timeLimit}
              onChange={tr.setTimeLimit}
            />
            <DateRangePicker
              numDays={tr.numDays}
              onPreset={tr.setDaysFromToday}
              onRange={tr.setRange}
            />
          </>
        )}
      </Page.Header>
      <Page.Body>
        <Page.Content>
          <div className="mb-4">
            <BackupPanel />
          </div>

          <div className="mb-4">
            <SourceHealthPanel />
          </div>

          {tab === "explorer" ? (
            <GroupableExplorer
              config={config}
              groupBy={groupBy}
              onGroupByChange={setGroupBy}
              resetKey={`${tr.startISO}|${tr.endISO}|${tr.timeLimit}|${entity}`}
              leafMode={mode}
            />
          ) : (
            <EntityExplorer />
          )}
        </Page.Content>
      </Page.Body>
    </Page>
  );
}
