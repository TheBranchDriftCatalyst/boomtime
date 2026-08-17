// Books — top-level page (gaka-books, merged onto <GroupableExplorer> in
// gaka-02sh Track C). A read-only window onto the siloed reading_items table:
// every tracked book + audiobook (Audible today, Kindle later), fused into one
// synthwave dashboard. No edit/delete here — the Amazon connect + sync/backfill
// controls live in Settings › Connections.
//
// The library table is the shared groupable explorer driven by the reading
// DomainConfig (booksExplorerConfig): groupBy [] is the flat book table (today's
// default view); adding a source/status/series/author/genre axis drills with
// count + runtime + finished rollups. The search / source / status filters fold
// into the DSL `where` (and the resetKey) so they constrain the grouping too.
import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import {
  BookMarked,
  Bookmark,
  BookOpen,
  Headphones,
  Library,
  Search,
} from "lucide-react";
import { Card, CardContent } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Page } from "@/layout/Page";
import { EmptyState } from "@/components/EmptyState";
import { runQuery } from "@/lib/queryApi";
import { qk } from "@/lib/queryKeys";
import { usePublicConfig } from "@/lib/usePublicConfig";
import { GroupableExplorer } from "@/features/explorer/GroupableExplorer";
import { GroupByBar } from "@/features/explorer/GroupByBar";
import { BookDetailSheet } from "@/features/books/BookDetailSheet";
import { BooksRefreshContext } from "@/features/books/booksRefresh";
import type { ReadingItemDTO } from "@/types/meta";
import {
  deriveHeroStats,
  HERO_SPEC,
  makeBooksExplorerConfig,
  makeHeroSpec,
  READING_AXES,
  STATUS_FILTER_OPTIONS,
  MATCH_FILTER_OPTIONS,
  type BooksFilters,
  type BooksHeroStats,
  type SourceFilter,
  type StatusFilter,
  type MatchFilter,
} from "@/features/books/booksExplorerConfig";

// ── hero + stats ─────────────────────────────────────────────────────────────

function StatChip({
  icon: Icon,
  label,
  value,
  filtered,
}: {
  icon: typeof Library;
  label: string;
  value: number;
  // When set (any filter is active), render `<filtered>/<total>` — the filtered
  // number prominent, the total muted. Undefined → just the total.
  filtered?: number;
}) {
  return (
    <div className="flex items-center gap-2.5 rounded-lg border border-primary/20 bg-primary/5 px-3 py-2">
      <span className="flex h-8 w-8 items-center justify-center rounded-md bg-primary/10 text-primary">
        <Icon className="h-4 w-4" />
      </span>
      <div className="leading-tight">
        <div className="text-lg font-semibold tabular-nums">
          {filtered != null ? (
            <>
              <span className="text-foreground">{filtered}</span>
              <span className="text-sm font-medium text-muted-foreground/60">
                /{value}
              </span>
            </>
          ) : (
            value
          )}
        </div>
        <div className="text-[11px] uppercase tracking-wide text-muted-foreground">
          {label}
        </div>
      </div>
    </div>
  );
}

function BooksHero({
  stats,
  filtered,
}: {
  stats: BooksHeroStats;
  // The filter-scoped counts, or null when no filter is active (show totals).
  filtered: BooksHeroStats | null;
}) {
  // Each card pairs a whole-library total with its filtered counterpart. When
  // `filtered` is null every chip shows just the total (today's behavior).
  const cards: Array<{
    icon: typeof Library;
    label: string;
    total: number;
    filtered?: number;
  }> = [
    { icon: Library, label: "Tracked", total: stats.total, filtered: filtered?.total },
    { icon: BookMarked, label: "Finished", total: stats.finished, filtered: filtered?.finished },
    { icon: Headphones, label: "Audible", total: stats.audible, filtered: filtered?.audible },
    { icon: BookOpen, label: "Kindle", total: stats.kindle, filtered: filtered?.kindle },
    { icon: Bookmark, label: "Hardcover", total: stats.hardcover, filtered: filtered?.hardcover },
  ];
  return (
    <div className="relative overflow-hidden rounded-xl border border-primary/20 bg-gradient-to-br from-primary/10 via-background to-background p-6">
      {/* neon bloom + faint grid — synthwave chrome, purely decorative */}
      <div className="pointer-events-none absolute -right-20 -top-24 h-56 w-56 rounded-full bg-primary/20 blur-3xl" />
      <div
        className="pointer-events-none absolute inset-0 opacity-[0.06]"
        style={{
          backgroundImage:
            "linear-gradient(hsl(var(--primary)) 1px, transparent 1px), linear-gradient(90deg, hsl(var(--primary)) 1px, transparent 1px)",
          backgroundSize: "28px 28px",
        }}
      />
      <div className="relative">
        <div className="flex items-center gap-1.5 font-mono text-[11px] uppercase tracking-[0.2em] text-primary/80">
          <Library className="h-3.5 w-3.5" />
          Reading log
        </div>
        <h2 className="mt-2 text-2xl font-semibold tracking-tight">
          Books & Audiobooks
        </h2>
        <p className="mt-1.5 max-w-xl text-sm text-muted-foreground">
          Everything boomtime tracks from your linked Amazon account — Audible
          listens today, Kindle reads next — plus books you shelve on Hardcover,
          fused into one library view.
        </p>
        <div className="mt-4 flex flex-wrap gap-2.5">
          {cards.map((c) => (
            <StatChip
              key={c.label}
              icon={c.icon}
              label={c.label}
              value={c.total}
              filtered={c.filtered}
            />
          ))}
        </div>
      </div>
    </div>
  );
}

// ── controls ─────────────────────────────────────────────────────────────────

// A native <select> styled to match the app's inputs (same treatment the Amazon
// connect card uses). Kept tiny + local — the Books page is the only caller.
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

// ── page ─────────────────────────────────────────────────────────────────────

const ZERO_STATS: BooksHeroStats = {
  total: 0,
  finished: 0,
  audible: 0,
  kindle: 0,
  hardcover: 0,
};

export function BooksPage() {
  const { config } = usePublicConfig();
  const booksEnabled = config.books_enabled;

  const [groupBy, setGroupBy] = useState<string[]>([]);
  const [search, setSearch] = useState("");
  const [sourceFilter, setSourceFilter] = useState<SourceFilter>("all");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [matchedFilter, setMatchedFilter] = useState<MatchFilter>("all");

  // Debounce the search box: search is now a server-side ILIKE predicate folded
  // into the explorer's `where` + resetKey, so typing would otherwise re-query
  // (dropping the explorer's caches) on every keystroke. The <Input> stays fully
  // responsive on `search`; only the debounced value drives the config/resetKey.
  const [debouncedSearch, setDebouncedSearch] = useState("");
  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search.trim()), 300);
    return () => clearTimeout(t);
  }, [search]);

  // The live filter selections, shared by the hero + the explorer.
  const filters: BooksFilters = useMemo(
    () => ({
      source: sourceFilter,
      status: statusFilter,
      matched: matchedFilter,
      search: debouncedSearch,
    }),
    [sourceFilter, statusFilter, matchedFilter, debouncedSearch],
  );
  const filtersActive =
    sourceFilter !== "all" ||
    statusFilter !== "all" ||
    matchedFilter !== "all" ||
    debouncedSearch !== "";

  // Hero summary: one unfiltered grouped query (group by source + finished
  // rollup) → whole-library totals. Only when the feature is on.
  const heroQuery = useQuery({
    queryKey: qk.booksHero(),
    queryFn: () => runQuery(HERO_SPEC),
    enabled: booksEnabled,
    staleTime: 60_000,
  });
  const heroStats =
    heroQuery.data?.kind === "groups"
      ? deriveHeroStats(heroQuery.data.groups)
      : ZERO_STATS;

  // Filter-scoped hero: the SAME source-grouped query with the active filters
  // folded into its where — feeds the `<filtered>/<total>` counts. Only runs
  // when a filter is active (an all-default filtered query would just duplicate
  // the totals). Keyed per filter combo so each caches independently.
  const filteredHeroQuery = useQuery({
    queryKey: qk.booksHero(filters),
    queryFn: () => runQuery(makeHeroSpec(filters)),
    enabled: booksEnabled && filtersActive,
    staleTime: 60_000,
  });
  const filteredHeroStats =
    filtersActive && filteredHeroQuery.data?.kind === "groups"
      ? deriveHeroStats(filteredHeroQuery.data.groups)
      : null;

  // Clicking a row opens the Book detail panel for that Work (all provider editions).
  const [selectedBook, setSelectedBook] = useState<ReadingItemDTO | null>(null);

  // Bumped by a mutation (status/rating/finished/match) to force the explorer to
  // refetch — it's not react-query backed, so it refreshes only when resetKey
  // changes (gaka-imeb). Folded into resetKey below; a callback goes to the cells.
  const [dataVersion, setDataVersion] = useState(0);
  const bumpData = useCallback(() => setDataVersion((v) => v + 1), []);

  // The reading DomainConfig, closed over the live filters; the resetKey folds
  // the same inputs so the explorer drops its caches on any filter change.
  const explorerConfig = useMemo(
    () => makeBooksExplorerConfig(filters, setSelectedBook),
    [filters],
  );
  const resetKey = `${sourceFilter}|${statusFilter}|${matchedFilter}|${debouncedSearch}|${dataVersion}`;

  return (
    <BooksRefreshContext.Provider value={bumpData}>
    <Page>
      <Page.Header title="Books" />
      <Page.Body>
        <Page.Content>
          <div className="space-y-6">
            <BooksHero stats={heroStats} filtered={filteredHeroStats} />

            {!booksEnabled ? (
              <Card>
                <CardContent className="pt-6">
                  <EmptyState
                    icon={Library}
                    title="Books isn't enabled on this server"
                    description="The books & audiobooks feature (BOOM_FEATURE_BOOKS) is turned off for this deployment."
                  />
                </CardContent>
              </Card>
            ) : (
              <div className="space-y-3">
                {/* ONE consolidated control bar — search + source + status +
                    group-by axis chips + connect, folded into a single tight row
                    that wraps gracefully on narrow widths. The filters fold into
                    the DSL where (source/status as eq leaves, the debounced
                    search as an ILIKE OR on title/author); the group-by chips are
                    the shared <GroupByBar> hosted here instead of by the explorer
                    (hideGroupByBar), so all controls live in one surface. */}
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
                  <FilterSelect<SourceFilter>
                    label="Source"
                    value={sourceFilter}
                    onChange={setSourceFilter}
                    options={[
                      { value: "all", label: "All" },
                      { value: "audible", label: "Audible" },
                      { value: "kindle", label: "Kindle" },
                      { value: "hardcover", label: "Hardcover" },
                    ]}
                  />
                  <FilterSelect<StatusFilter>
                    label="Status"
                    value={statusFilter}
                    onChange={setStatusFilter}
                    options={STATUS_FILTER_OPTIONS}
                  />
                  <FilterSelect<MatchFilter>
                    label="Match"
                    value={matchedFilter}
                    onChange={setMatchedFilter}
                    options={MATCH_FILTER_OPTIONS}
                  />
                  {/* Subtle divider between the filters and the group-by axes —
                      hidden when the row wraps to avoid a dangling rule. */}
                  <span
                    aria-hidden
                    className="hidden h-6 w-px shrink-0 bg-border md:block"
                  />
                  <GroupByBar
                    axes={READING_AXES}
                    groupBy={groupBy}
                    onChange={setGroupBy}
                  />
                  <Button asChild size="sm" variant="outline" className="ml-auto">
                    <Link to="/app/settings?tab=connections">
                      <Library className="mr-1.5 h-4 w-4" />
                      Connect Amazon
                    </Link>
                  </Button>
                </div>

                <GroupableExplorer
                  config={explorerConfig}
                  groupBy={groupBy}
                  onGroupByChange={setGroupBy}
                  resetKey={resetKey}
                  hideGroupByBar
                />
              </div>
            )}
          </div>
        </Page.Content>
      </Page.Body>
      <BookDetailSheet
        item={selectedBook}
        onOpenChange={(open) => !open && setSelectedBook(null)}
      />
    </Page>
    </BooksRefreshContext.Provider>
  );
}

export default BooksPage;
