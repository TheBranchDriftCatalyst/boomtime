// booksExplorerConfig.tsx — the reading-domain DomainConfig for the shared
// <GroupableExplorer> (gaka-02sh Track C). This is what merges the old Books
// "Table" + "Explore" tabs into ONE groupable table:
//   - groupBy [] (default) → the flat leaf-rows table users see today (the 8/9
//     book columns), fetched via the query DSL rows mode (NOT api.getBooksItems).
//   - add an axis (source/status/series/author/genre) → server-side group-by
//     drill-down with count + runtime + finished rollups per group.
//
// Everything is driven by the cross-domain query DSL (runQuery): fetchGroup runs
// a grouped `books` query with rollups; fetchLeaf runs the DSL `rows` mode. The
// page's search / source / status filters ALL fold into each query's `where` (so
// they constrain BOTH the group aggregates and the leaf rows, fully server-side)
// — see buildWhere. Search compiles to an OR of case-insensitive ILIKE substring
// matches on title + author (gaka-02sh P2 follow-up).
import type { ReactNode } from "react";
import { ExternalLink } from "lucide-react";
import { Library } from "lucide-react";
import { EmptyState } from "@/components/EmptyState";
import { runQuery, type PredicateNode, type QuerySpec } from "@/lib/queryApi";
import {
  AuthorCell,
  Cover,
  FinishedEditor,
  HardcoverBadge,
  ProgressBar,
  RatingEditor,
  SourceBadge,
  STATUS_META,
  StatusSelect,
  TitleCell,
} from "@/features/books/cells";
import { BOOK_STATUSES, type BookStatus } from "@/types/meta";
import { openHardcover } from "@/features/books/hardcover";
import type {
  Axis,
  Column,
  DomainConfig,
  DrillPath,
  GroupPage,
  LeafResult,
  Rollup,
} from "@/features/explorer/types";
import type { ReadingItemDTO } from "@/types/meta";

// The page-level filter selections. `all` = no constraint. ALL three fold into
// every query's `where` (source/status as eq leaves, search as an ILIKE OR on
// title/author) — see buildWhere. Nothing is filtered client-side.
// `hardcover` = books shelved on Hardcover but not owned on Kindle/Audible
// (source='hardcover' reading_items, ingested by the Hardcover backfill).
export type SourceFilter = "all" | "audible" | "kindle" | "hardcover";
// gaka-books: the status filter now speaks the ONE canonical vocabulary (1:1
// with Hardcover) so filter value == group value == pill key. Was the
// mismatched all|reading|finished|want, where "finished" was a mislabel of the
// `read` column value — the filter and the group-by axis disagreed.
export type StatusFilter = "all" | BookStatus;

// MatchFilter is the Hardcover MATCH/SYNC-STATE meta-status (a facet of sync/linkage
// state, sibling to the reading status — not a reading-progress value).
//   matched/unmatched → the isMatched dim (linked to Hardcover at all).
//   diverged          → the syncState dim ('diverged' = matched but effective status
//                       disagrees with the last-seen Hardcover shelf = a pending sync
//                       change = the amber diff badge). "Out of sync" in the UI.
export type MatchFilter = "all" | "matched" | "unmatched" | "diverged";

export interface BooksFilters {
  source: SourceFilter;
  status: StatusFilter;
  matched: MatchFilter;
  search: string;
}

// How many named groups to keep before the backend rolls the rest into "Other".
export const TOP_N = 12;

// Leaf page size. The flat (groupBy=[]) view renders a single page as its whole
// table (the generic explorer paginates only under a drilled group), so this is
// also the flat-view cap — kept generous so a personal library shows in full.
export const LEAF_PAGE_SIZE = 250;

/** Compact "runtime minutes" → "1h 20m" / "45m" / "3h". */
export function formatMinutes(min: number): string {
  const total = Math.max(0, Math.round(min));
  if (total < 60) return `${total}m`;
  const h = Math.floor(total / 60);
  const m = total % 60;
  return m ? `${h}h ${m}m` : `${h}h`;
}

// --- Group axes --------------------------------------------------------------

// The reading dimensions offered in the "Group by" bar. Mirrors the backend
// registry whitelist for the `books` measure (internal/query/domains.go):
// source / status / series / author / genre.
export const READING_AXES: Axis[] = [
  { id: "source", label: "Source" },
  // EFFECTIVE status (override ?? Amazon-derived) — the same axis the filter
  // reads and goals/rollups run on.
  { id: "status", label: "Status" },
  // NOTE: the raw Amazon-derived status axis ("Status (Amazon)", dim `statusDerived`)
  // was removed from the default axes — status is 1:1 with the Hardcover shelf now, so
  // exposing BOTH the effective and the raw-Amazon status overloaded the concept. The
  // `statusDerived` DSL dimension still exists (diagnostic) but isn't a headline axis.
  // Hardcover match-state — a meta-status facet (linked to Hardcover vs not),
  // groupable like any status axis so you can split the library by sync state.
  { id: "isMatched", label: "Match state" },
  // Finer sync facet: unmatched / synced / diverged (out of sync with Hardcover).
  { id: "syncState", label: "Sync state" },
  { id: "series", label: "Series" },
  { id: "author", label: "Author" },
  { id: "genre", label: "Genre" },
];

// --- Leaf columns ------------------------------------------------------------

// guardClick wraps an interactive cell (status dropdown, rating, finished editor,
// match-fixer) so its clicks don't bubble to the row-level detail-panel open.
// Non-interactive cells (title/author/source) let the click through so tapping
// the row opens the Book panel.
const guardClick = (node: ReactNode) => (
  <span onClick={(e) => e.stopPropagation()}>{node}</span>
);

// The 8 book columns (Cover spans its own cell, so 9 Column entries). `render`
// draws the cell via the shared cells.tsx components; `get` is the sort key.
export const BOOK_COLUMNS: Column<ReadingItemDTO>[] = [
  {
    id: "cover",
    header: "",
    get: (r) => r.coverUrl ?? "",
    render: (r) => <Cover item={r} />,
    defaultVisible: true,
  },
  {
    id: "title",
    header: "Title",
    get: (r) => r.title,
    render: (r) => <TitleCell item={r} />,
    cellTitle: (r) => r.title,
    defaultVisible: true,
  },
  {
    id: "author",
    header: "Author",
    get: (r) => r.authors,
    render: (r) => <AuthorCell item={r} />,
    cellTitle: (r) => r.authors,
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
    get: (r) => r.status,
    // Editable: the pill becomes a StatusSelect dropdown (curation override →
    // Hardcover) with a provenance dot. Sort key stays the effective status.
    render: (r) => guardClick(<StatusSelect item={r} />),
    defaultVisible: true,
  },
  {
    id: "hardcover",
    header: "Hardcover",
    // Matched rows sort ahead of unmatched (a resolved id > 0).
    get: (r) => (r.hardcoverBookId != null ? 1 : 0),
    render: (r) => guardClick(<HardcoverBadge item={r} />),
    defaultVisible: true,
  },
  {
    id: "progress",
    header: "Progress",
    get: (r) => r.progressPercent,
    render: (r) => <ProgressBar pct={r.progressPercent} />,
    defaultVisible: true,
  },
  {
    id: "finished",
    header: "Finished",
    // Recently-finished first; items without a finish date sink to the end.
    get: (r) => (r.finishedAt ? Date.parse(r.finishedAt) : -Infinity),
    // Editable: inline calendar popover writes the finished_at override.
    render: (r) => guardClick(<FinishedEditor item={r} />),
    cellClassName: "whitespace-nowrap",
    defaultVisible: true,
  },
  {
    id: "rating",
    header: "Rating",
    get: (r) => r.rating ?? r.goodreadsRating ?? 0,
    // Editable: inline 1..5 star editor writes the rating override.
    render: (r) => guardClick(<RatingEditor item={r} />),
    defaultVisible: true,
  },
];

// --- Rollups -----------------------------------------------------------------

// Per-group aggregates shown inline alongside the implicit `count`. Their ids
// are the DSL rollup measure names requested in fetchGroup.
export const BOOK_ROLLUPS: Rollup[] = [
  { id: "runtime", label: "Runtime", format: formatMinutes },
  { id: "finished", label: "Finished", format: (n) => n.toLocaleString() },
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
 * undefined (no constraint). This is the server-side replacement for the old
 * client-side page-only filter — because it's a `where` predicate it constrains
 * the group aggregates (fetchGroup) AND every leaf row (fetchLeaf), not just the
 * fetched page.
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
 * Map a drill path to an AND of dimension-equality leaves. A null step is
 * dropped (the backend's absent = "no filter" convention). Returns undefined
 * for an empty/all-null path (no constraint).
 */
export function pathToPredicate(path: DrillPath): PredicateNode | undefined {
  return andAll(
    path
      .filter((s) => s.value != null)
      .map((s) => eqLeaf(s.dim, s.value as string)),
  );
}

/**
 * Map the page's source/status/search selections to `where` leaves.
 * status folds directly onto the EFFECTIVE `status` dimension using the ONE
 * canonical value (gaka-books) — the filter value IS the column/group value now
 * (no more "finished" → "read" remap), so filter and group-by can't disagree.
 * `search` folds to an ILIKE OR on title/author (searchToPredicate) — the DSL
 * has a substring op, so free-text search is a real server predicate that
 * constrains both the group aggregates and the leaf rows.
 */
export function filtersToPredicate(filters: BooksFilters): PredicateNode[] {
  const leaves: PredicateNode[] = [];
  if (filters.source !== "all") leaves.push(eqLeaf("source", filters.source));
  if (filters.status !== "all") leaves.push(eqLeaf("status", filters.status));
  if (filters.matched === "diverged") {
    leaves.push(eqLeaf("syncState", "diverged"));
  } else if (filters.matched !== "all") {
    leaves.push(eqLeaf("isMatched", filters.matched));
  }
  const searchNode = searchToPredicate(filters.search);
  if (searchNode) leaves.push(searchNode);
  return leaves;
}

// The canonical status filter options — value == the `status` group value == the
// pill key, label == the pill label (STATUS_META). Driven off BOOK_STATUSES so
// the filter can never drift from the pills/groups. `all` leads.
export const STATUS_FILTER_OPTIONS: Array<{ value: StatusFilter; label: string }> =
  [
    { value: "all", label: "All" },
    ...BOOK_STATUSES.map((s) => ({ value: s, label: STATUS_META[s].label })),
  ];

// The Hardcover match-state (meta-status) filter options — value == the
// `isMatched` group value, so filter == group == the badge state.
export const MATCH_FILTER_OPTIONS: Array<{ value: MatchFilter; label: string }> = [
  { value: "all", label: "All" },
  { value: "matched", label: "Matched" },
  { value: "unmatched", label: "Not matched" },
  { value: "diverged", label: "Out of sync" },
];

/** Combine a drill path + the page filters into one `where` predicate. */
export function buildWhere(
  path: DrillPath,
  filters: BooksFilters,
): PredicateNode | undefined {
  const pathLeaves = path
    .filter((s) => s.value != null)
    .map((s) => eqLeaf(s.dim, s.value as string));
  return andAll([...pathLeaves, ...filtersToPredicate(filters)]);
}

// --- Config ------------------------------------------------------------------

const EMPTY_STATE = (
  <EmptyState
    icon={Library}
    title="Nothing to show"
    description="No tracked books match your current search and filters. Clear a filter, or connect your Amazon account and run a backfill to import your library."
  />
);

/**
 * Build the reading DomainConfig for <GroupableExplorer>, closed over the page's
 * current filter selections. Rebuilt whenever a filter changes so the source
 * adapter always queries with the live `where` — the page pairs this with a
 * matching resetKey so the explorer drops its caches on any filter change.
 */
export function makeBooksExplorerConfig(
  filters: BooksFilters,
  onRowSelect?: (row: ReadingItemDTO) => void,
): DomainConfig<ReadingItemDTO> {
  const source = {
    fetchGroup: async (
      path: DrillPath,
      axis: string,
      rollups: string[],
    ): Promise<GroupPage> => {
      const spec: QuerySpec = {
        domain: "reading",
        measure: "books",
        group: axis,
        where: buildWhere(path, filters),
        rollups,
        // No "Other" catch-all for Books: an aggregate 'Other' bucket isn't a real
        // dimension value, so it drills into nothing (gaka-a6nc). We return every
        // real group sorted by count desc; the legitimate null group renders as
        // "(none)". (High-cardinality axes like author just return more real rows.)
        sort: { field: "value", desc: true },
      };
      const res = await runQuery(spec);
      const groups = res.kind === "groups" ? res.groups : [];
      return {
        groups: groups.map((g) => ({
          // "" = the null dimension value → render as "(none)" + skip its drill
          // step (matches the generic explorer's null-filter convention).
          value: g.key === "" ? null : g.key,
          stats: {
            count: g.count ?? g.stats?.count ?? g.value ?? 0,
            runtime: g.stats?.runtime ?? 0,
            finished: g.stats?.finished ?? 0,
          },
        })),
        // The DSL rolls the tail into an "Other" bucket rather than truncating,
        // so there is no separate truncated signal to surface.
        truncated: false,
      };
    },
    fetchLeaf: async (
      path: DrillPath,
      page: number,
      pageSize: number,
    ): Promise<LeafResult<ReadingItemDTO>> => {
      const spec: QuerySpec = {
        domain: "reading",
        measure: "books", // ignored in rows mode, but the spec type requires it
        rows: true,
        where: buildWhere(path, filters),
        page: { number: page, size: pageSize },
      };
      const res = await runQuery(spec);
      // The reading RowSource projects keys 1:1 with ReadingItemDTO (see
      // internal/query/domains.go), so rows cast directly. Search is now a
      // server-side ILIKE predicate folded into `where` (buildWhere), so there is
      // no client-side page filtering here — total reflects the whole matching set.
      const rows =
        res.kind === "rows" ? (res.rows as unknown as ReadingItemDTO[]) : [];
      const total = res.kind === "rows" ? res.total : rows.length;
      return { rows, total, page, limit: pageSize };
    },
  };

  return {
    axes: READING_AXES,
    defaultGroupBy: [],
    columns: BOOK_COLUMNS,
    rollups: BOOK_ROLLUPS,
    source,
    rowKey: (r) => `${r.source}:${r.externalId}`,
    // Clicking a row opens the Book detail panel (the Work across all providers).
    onRowSelect,
    leafPageSize: LEAF_PAGE_SIZE,
    labels: {
      leafGroup: "Books",
      treeHeader: "Group",
      // No addAxisHint → zero axes renders the flat leaf table (today's default).
      loadError: "Failed to load your library.",
      empty: EMPTY_STATE,
    },
    // Whole-row click isn't a surface the shared LeafRow exposes; a trailing
    // per-row action opens the book's Hardcover page instead.
    rowActions: (r) => (
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          openHardcover(r);
        }}
        title={`Open ${r.title} on Hardcover`}
        aria-label={`Open ${r.title} on Hardcover`}
        className="rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"
      >
        <ExternalLink className="h-3.5 w-3.5" />
      </button>
    ),
    // Books drags ZERO curation — no group decorator.
  };
}

// --- Hero stats --------------------------------------------------------------
//
// The header summary cards (Tracked / Finished / Audible / Kindle / Hardcover)
// are derived from a source-grouped query — group by source with a `finished`
// rollup. The page runs it twice: bare (HERO_SPEC) for the whole-library totals,
// and filter-scoped (makeHeroSpec) so each card can show `<filtered>/<total>`.
// Kept DSL-driven so the page no longer needs api.getBooksItems at all.

export interface BooksHeroStats {
  total: number;
  finished: number;
  audible: number;
  kindle: number;
  hardcover: number;
}

export const HERO_SPEC: QuerySpec = {
  domain: "reading",
  measure: "books",
  group: "source",
  rollups: ["finished"],
};

/**
 * The hero query with the page filters folded into its `where` — the same
 * source/status/search predicates the explorer uses (filtersToPredicate). Feeds
 * the FILTER-scoped hero counts so each card can render `<filtered>/<total>`.
 * With no active filter the where collapses to undefined (== HERO_SPEC).
 */
export function makeHeroSpec(filters: BooksFilters): QuerySpec {
  return { ...HERO_SPEC, where: andAll(filtersToPredicate(filters)) };
}

export function deriveHeroStats(
  groups: { key: string; count?: number; value: number; stats?: Record<string, number> }[],
): BooksHeroStats {
  let total = 0;
  let finished = 0;
  let audible = 0;
  let kindle = 0;
  let hardcover = 0;
  for (const g of groups) {
    const count = g.count ?? g.stats?.count ?? g.value ?? 0;
    total += count;
    finished += g.stats?.finished ?? 0;
    const key = g.key.toLowerCase();
    if (key === "audible") audible += count;
    else if (key === "kindle") kindle += count;
    else if (key === "hardcover") hardcover += count;
  }
  return { total, finished, audible, kindle, hardcover };
}
