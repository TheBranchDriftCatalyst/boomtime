// readingEventsExplorerConfig.tsx — the readingEvents-domain DomainConfig for the
// shared <GroupableExplorer> (gaka-z5dz). Sibling to booksExplorerConfig, but over
// the `reads` measure (count of discrete reads) on the reading_events_enriched view
// instead of the `books` measure on reading_items:
//   - groupBy [] (default) → the flat event table (one row per READ: title, origin,
//     source, finished date), fetched via the query DSL rows mode.
//   - add an axis (origin/source/series/author/genre/status) → server-side group-by
//     with a per-group read count.
//
// A book read three times is THREE rows here (three reads), whereas the library tab
// shows it as ONE book — that is the whole point of the events surface. Everything
// is driven by the same cross-domain query DSL (runQuery): fetchGroup runs a grouped
// `reads` query; fetchLeaf runs the DSL `rows` mode over the events RowSource. The
// tab's search / source / origin / status filters fold into each query's `where`
// (buildWhere), so they constrain BOTH the group counts and the leaf rows.
import { ExternalLink } from "lucide-react";
import { CalendarClock } from "lucide-react";
import { EmptyState } from "@shared/components/EmptyState";
import { runQuery, type PredicateNode, type QuerySpec } from "@shared/lib/queryApi";
import { fmtDate, SourceBadge, StatusPill } from "@books/features/books/cells";
import type {
  Axis,
  Column,
  DomainConfig,
  DrillPath,
  GroupPage,
  LeafResult,
} from "@shared/features/explorer/types";
import type { ReadingEventRowDTO } from "@shared/types/meta";

// The events-tab filter selections. `all` = no constraint. All fold into every
// query's `where` (source/origin/status as eq leaves, search as an ILIKE OR on
// title/author). Origin (who produced the read) is the events-only facet; source
// is the Amazon edition kind (empty for a hardcover-only read).
export type EventSourceFilter = "all" | "audible" | "kindle" | "hardcover";
export type EventOriginFilter = "all" | "audible" | "kindle" | "hardcover";
export type EventStatusFilter = "all" | "want" | "reading" | "read" | "paused" | "dnf";

export interface ReadingEventsFilters {
  source: EventSourceFilter;
  origin: EventOriginFilter;
  status: EventStatusFilter;
  search: string;
}

export const NO_EVENT_FILTERS: ReadingEventsFilters = {
  source: "all",
  origin: "all",
  status: "all",
  search: "",
};

// Leaf page size — the flat (groupBy=[]) view renders one page as its whole table,
// so this doubles as the flat-view cap. Kept generous for a personal read history.
export const EVENT_LEAF_PAGE_SIZE = 250;

// --- Group axes --------------------------------------------------------------

// The event dimensions offered in the "Group by" bar. Mirrors the backend registry
// whitelist for the `reads` measure (internal/shared/query/domains.go
// registerReadingEvents): origin / source / series / author / genre / status.
export const READING_EVENT_AXES: Axis[] = [
  // origin — who produced the read (Hardcover / Audible / Kindle). The events-only
  // axis; a read's provenance, distinct from the book's Amazon edition (source).
  { id: "origin", label: "Origin" },
  { id: "source", label: "Source" },
  { id: "series", label: "Series" },
  { id: "author", label: "Author" },
  { id: "genre", label: "Genre" },
  // EFFECTIVE status of the underlying book (override ?? item status).
  { id: "status", label: "Status" },
];

// --- Leaf columns ------------------------------------------------------------

// The event-row columns (book title, origin, source, finished date). status is a
// bonus toggleable column. `get` is the sort key; `render` draws the cell.
export const EVENT_COLUMNS: Column<ReadingEventRowDTO>[] = [
  {
    id: "title",
    header: "Title",
    get: (r) => r.title ?? "",
    render: (r) => (
      <div className="min-w-0">
        <div className="truncate font-medium text-foreground">
          {r.title ?? "—"}
        </div>
        {r.authors ? (
          <div className="truncate text-xs text-muted-foreground">
            {r.authors}
          </div>
        ) : null}
      </div>
    ),
    cellTitle: (r) => r.title ?? undefined,
    defaultVisible: true,
  },
  {
    id: "origin",
    header: "Origin",
    get: (r) => r.origin,
    render: (r) => <SourceBadge source={r.origin} />,
    defaultVisible: true,
  },
  {
    id: "source",
    header: "Source",
    get: (r) => r.source,
    render: (r) => <SourceBadge source={r.source} />,
    defaultVisible: true,
  },
  {
    id: "status",
    header: "Status",
    get: (r) => r.status ?? "",
    render: (r) => <StatusPill status={r.status ?? ""} finished={r.status === "read"} />,
    // Not default-visible — the events table leads with the read fields; toggle
    // status on via Columns when you want the underlying book's shelf state.
    defaultVisible: false,
  },
  {
    id: "finished",
    header: "Finished",
    // Recently-finished first; reads without a finish date sink to the end.
    get: (r) => (r.finishedAt ? Date.parse(r.finishedAt) : -Infinity),
    render: (r) => (
      <span className="whitespace-nowrap">{fmtDate(r.finishedAt ?? undefined)}</span>
    ),
    cellClassName: "whitespace-nowrap",
    defaultVisible: true,
  },
];

// --- where predicate builders (exported for tests) ---------------------------

function eqLeaf(dim: string, value: string): PredicateNode {
  return { kind: "leaf", dim, op: "eq", values: [value] };
}

function ilikeLeaf(dim: string, value: string): PredicateNode {
  return { kind: "leaf", dim, op: "ilike", values: [value] };
}

/**
 * Map a free-text search term to a server-side predicate: a case-insensitive
 * substring (ILIKE) match on EITHER title OR author. Whitespace-only / empty →
 * undefined (no constraint). Because it's a `where` predicate it constrains the
 * group counts (fetchGroup) AND every leaf row (fetchLeaf).
 */
export function searchToPredicate(search: string): PredicateNode | undefined {
  const q = search.trim();
  if (!q) return undefined;
  return { kind: "or", of: [ilikeLeaf("title", q), ilikeLeaf("author", q)] };
}

function andAll(nodes: PredicateNode[]): PredicateNode | undefined {
  if (nodes.length === 0) return undefined;
  if (nodes.length === 1) return nodes[0];
  return { kind: "and", of: nodes };
}

/**
 * Map a drill path to an AND of dimension-equality leaves. A null step is dropped
 * (the backend's absent = "no filter" convention). Returns undefined for an
 * empty/all-null path.
 */
export function pathToPredicate(path: DrillPath): PredicateNode | undefined {
  return andAll(
    path
      .filter((s) => s.value != null)
      .map((s) => eqLeaf(s.dim, s.value as string)),
  );
}

/** Map the tab's source/origin/status/search selections to `where` leaves. */
export function filtersToPredicate(filters: ReadingEventsFilters): PredicateNode[] {
  const leaves: PredicateNode[] = [];
  if (filters.source !== "all") leaves.push(eqLeaf("source", filters.source));
  if (filters.origin !== "all") leaves.push(eqLeaf("origin", filters.origin));
  if (filters.status !== "all") leaves.push(eqLeaf("status", filters.status));
  const searchNode = searchToPredicate(filters.search);
  if (searchNode) leaves.push(searchNode);
  return leaves;
}

/** Combine a drill path + the tab filters into one `where` predicate. */
export function buildWhere(
  path: DrillPath,
  filters: ReadingEventsFilters,
): PredicateNode | undefined {
  const pathLeaves = path
    .filter((s) => s.value != null)
    .map((s) => eqLeaf(s.dim, s.value as string));
  return andAll([...pathLeaves, ...filtersToPredicate(filters)]);
}

// --- Config ------------------------------------------------------------------

const EMPTY_STATE = (
  <EmptyState
    icon={CalendarClock}
    title="No reading events yet"
    description="Nothing matches your current search and filters. Reading events are the discrete reads boomtime detects from Hardcover, Audible, and Kindle — connect a source and run a backfill to populate the timeline."
  />
);

/**
 * Build the readingEvents DomainConfig for <GroupableExplorer>, closed over the
 * tab's current filter selections. Rebuilt whenever a filter changes so the source
 * adapter always queries with the live `where`; the tab pairs this with a matching
 * resetKey so the explorer drops its caches on any filter change.
 */
export function makeReadingEventsExplorerConfig(
  filters: ReadingEventsFilters,
): DomainConfig<ReadingEventRowDTO> {
  const source = {
    fetchGroup: async (
      path: DrillPath,
      axis: string,
      _rollups: string[],
    ): Promise<GroupPage> => {
      const spec: QuerySpec = {
        domain: "readingEvents",
        measure: "reads",
        group: axis,
        where: buildWhere(path, filters),
        // No "Other" catch-all: an aggregate 'Other' bucket drills into nothing.
        // Every real group is returned sorted by read-count desc; the legitimate
        // null group renders as "(none)".
        sort: { field: "value", desc: true },
      };
      const res = await runQuery(spec);
      const groups = res.kind === "groups" ? res.groups : [];
      return {
        groups: groups.map((g) => ({
          value: g.key === "" ? null : g.key,
          // `reads` has no rollups — the group's read count is the measure value
          // (g.value), mirrored into stats.count for the generic explorer.
          stats: { count: g.count ?? g.stats?.count ?? g.value ?? 0 },
        })),
        truncated: false,
      };
    },
    fetchLeaf: async (
      path: DrillPath,
      page: number,
      pageSize: number,
    ): Promise<LeafResult<ReadingEventRowDTO>> => {
      const spec: QuerySpec = {
        domain: "readingEvents",
        measure: "reads", // ignored in rows mode, but the spec type requires it
        rows: true,
        where: buildWhere(path, filters),
        page: { number: page, size: pageSize },
      };
      const res = await runQuery(spec);
      // The readingEvents RowSource projects keys 1:1 with ReadingEventRowDTO
      // (internal/shared/query/domains.go), so rows cast directly.
      const rows =
        res.kind === "rows" ? (res.rows as unknown as ReadingEventRowDTO[]) : [];
      const total = res.kind === "rows" ? res.total : rows.length;
      return { rows, total, page, limit: pageSize };
    },
  };

  return {
    axes: READING_EVENT_AXES,
    defaultGroupBy: [],
    columns: EVENT_COLUMNS,
    // `reads` is a pure count — no extra per-group rollups.
    rollups: [],
    source,
    rowKey: (r) =>
      `${r.origin}:${r.source}:${r.externalId}:${r.hardcoverBookId ?? ""}:${r.startedAt ?? ""}:${r.finishedAt ?? ""}`,
    leafPageSize: EVENT_LEAF_PAGE_SIZE,
    labels: {
      leafGroup: "Reads",
      treeHeader: "Group",
      // No addAxisHint → zero axes renders the flat event table (the default view).
      loadError: "Failed to load your reading events.",
      empty: EMPTY_STATE,
    },
    // A trailing per-row action: open the underlying book on Hardcover when known.
    rowActions: (r) =>
      r.hardcoverBookId != null ? (
        <a
          href={`https://hardcover.app/books/${r.hardcoverBookId}`}
          target="_blank"
          rel="noreferrer"
          onClick={(e) => e.stopPropagation()}
          title={`Open ${r.title ?? "book"} on Hardcover`}
          aria-label={`Open ${r.title ?? "book"} on Hardcover`}
          className="rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"
        >
          <ExternalLink className="h-3.5 w-3.5" />
        </a>
      ) : null,
  };
}
