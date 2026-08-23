// ReadingEventsTab.tsx — the "Reading Events" tab body on the Books page
// (boom-z5dz). The same <GroupableExplorer> axis-grouping table the Library tab
// uses, but driven by the readingEvents DomainConfig (the `reads` measure over the
// reading_events_enriched view): one row per discrete READ, groupable by
// origin/source/series/author/genre/status.
//
// Self-contained: it owns its own search / source / origin / status / group / sort
// state (local, not URL-persisted — only the ACTIVE TAB is persisted by BooksPage
// via ?view=events, so the two tabs never fight over the same ?group key). The
// filters fold into the DSL `where` + the explorer resetKey exactly like the
// library tab.
import { useEffect, useMemo, useState } from "react";
import { Search } from "lucide-react";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { GroupByBar } from "@shared/features/explorer/GroupByBar";
import { GroupableExplorer } from "@shared/features/explorer/GroupableExplorer";
import type { LeafSort } from "@shared/features/explorer/useLeafSort";
import {
  makeReadingEventsExplorerConfig,
  READING_EVENT_AXES,
  type EventOriginFilter,
  type EventSourceFilter,
  type EventStatusFilter,
  type ReadingEventsFilters,
} from "@books/features/books/readingEventsExplorerConfig";

// A native <select> styled to match the app's inputs — a tiny local copy of the
// library tab's FilterSelect so this tab stays self-contained (and the library tab
// stays byte-identical).
function FilterSelect<T extends string>({
  label,
  value,
  onChange,
  options,
}: {
  label: string;
  value: T;
  onChange: (v: T) => void;
  options: Array<{ value: T; label: string }>;
}) {
  return (
    <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
      <span className="shrink-0">{label}</span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value as T)}
        className="rounded-md border border-border bg-background px-2 py-1.5 text-sm text-foreground"
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </label>
  );
}

const SOURCE_OPTIONS: Array<{ value: EventSourceFilter; label: string }> = [
  { value: "all", label: "All" },
  { value: "audible", label: "Audible" },
  { value: "kindle", label: "Kindle" },
  { value: "hardcover", label: "Hardcover" },
];
const ORIGIN_OPTIONS: Array<{ value: EventOriginFilter; label: string }> = [
  { value: "all", label: "All" },
  { value: "hardcover", label: "Hardcover" },
  { value: "audible", label: "Audible" },
  { value: "kindle", label: "Kindle" },
];
const STATUS_OPTIONS: Array<{ value: EventStatusFilter; label: string }> = [
  { value: "all", label: "All" },
  { value: "want", label: "Want" },
  { value: "reading", label: "Reading" },
  { value: "read", label: "Finished" },
  { value: "paused", label: "Paused" },
  { value: "dnf", label: "DNF" },
];

export function ReadingEventsTab() {
  const [groupBy, setGroupBy] = useState<string[]>([]);
  const [search, setSearch] = useState("");
  const [sourceFilter, setSourceFilter] = useState<EventSourceFilter>("all");
  const [originFilter, setOriginFilter] = useState<EventOriginFilter>("all");
  const [statusFilter, setStatusFilter] = useState<EventStatusFilter>("all");
  const [sort, setSort] = useState<LeafSort | null>(null);

  // Debounce the search box: search is a server-side ILIKE predicate folded into
  // the explorer's `where` + resetKey, so typing would otherwise re-query on every
  // keystroke. The <Input> stays responsive on `search`; the debounced value drives
  // the config/resetKey.
  const [debouncedSearch, setDebouncedSearch] = useState("");
  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search.trim()), 300);
    return () => clearTimeout(t);
  }, [search]);

  const filters: ReadingEventsFilters = useMemo(
    () => ({
      source: sourceFilter,
      origin: originFilter,
      status: statusFilter,
      search: debouncedSearch,
    }),
    [sourceFilter, originFilter, statusFilter, debouncedSearch],
  );

  const explorerConfig = useMemo(
    () => makeReadingEventsExplorerConfig(filters),
    [filters],
  );
  const resetKey = `${sourceFilter}|${originFilter}|${statusFilter}|${debouncedSearch}`;

  return (
    <div className="space-y-3">
      {/* One consolidated control bar — search + source + origin + status +
          group-by axis chips, folded into a single tight row (mirrors the library
          tab). The group-by chips are the shared <GroupByBar> hosted here
          (hideGroupByBar on the explorer). */}
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-xl border border-primary/15 bg-card/40 px-3 py-2.5">
        <div className="relative min-w-[200px] flex-1">
          <Search className="pointer-events-none absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search title or author…"
            className="pl-8"
          />
        </div>
        <FilterSelect<EventOriginFilter>
          label="Origin"
          value={originFilter}
          onChange={setOriginFilter}
          options={ORIGIN_OPTIONS}
        />
        <FilterSelect<EventSourceFilter>
          label="Source"
          value={sourceFilter}
          onChange={setSourceFilter}
          options={SOURCE_OPTIONS}
        />
        <FilterSelect<EventStatusFilter>
          label="Status"
          value={statusFilter}
          onChange={setStatusFilter}
          options={STATUS_OPTIONS}
        />
        <span
          aria-hidden
          className="hidden h-6 w-px shrink-0 bg-border md:block"
        />
        <GroupByBar
          axes={READING_EVENT_AXES}
          groupBy={groupBy}
          onChange={setGroupBy}
        />
      </div>

      <GroupableExplorer
        config={explorerConfig}
        groupBy={groupBy}
        onGroupByChange={setGroupBy}
        resetKey={resetKey}
        hideGroupByBar
        sort={sort}
        onSortChange={setSort}
      />
    </div>
  );
}

export default ReadingEventsTab;
