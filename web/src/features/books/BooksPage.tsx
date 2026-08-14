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
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import {
  BookMarked,
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
import {
  deriveHeroStats,
  HERO_SPEC,
  makeBooksExplorerConfig,
  STATUS_FILTER_OPTIONS,
  type BooksHeroStats,
  type SourceFilter,
  type StatusFilter,
} from "@/features/books/booksExplorerConfig";

// ── hero + stats ─────────────────────────────────────────────────────────────

function StatChip({
  icon: Icon,
  label,
  value,
}: {
  icon: typeof Library;
  label: string;
  value: number;
}) {
  return (
    <div className="flex items-center gap-2.5 rounded-lg border border-primary/20 bg-primary/5 px-3 py-2">
      <span className="flex h-8 w-8 items-center justify-center rounded-md bg-primary/10 text-primary">
        <Icon className="h-4 w-4" />
      </span>
      <div className="leading-tight">
        <div className="text-lg font-semibold tabular-nums">{value}</div>
        <div className="text-[11px] uppercase tracking-wide text-muted-foreground">
          {label}
        </div>
      </div>
    </div>
  );
}

function BooksHero({ stats }: { stats: BooksHeroStats }) {
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
          listens today, Kindle reads next — fused into one library view.
        </p>
        <div className="mt-4 flex flex-wrap gap-2.5">
          <StatChip icon={Library} label="Tracked" value={stats.total} />
          <StatChip icon={BookMarked} label="Finished" value={stats.finished} />
          <StatChip icon={Headphones} label="Audible" value={stats.audible} />
          <StatChip icon={BookOpen} label="Kindle" value={stats.kindle} />
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
};

export function BooksPage() {
  const { config } = usePublicConfig();
  const booksEnabled = config.books_enabled;

  const [groupBy, setGroupBy] = useState<string[]>([]);
  const [search, setSearch] = useState("");
  const [sourceFilter, setSourceFilter] = useState<SourceFilter>("all");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");

  // Debounce the search box: search is now a server-side ILIKE predicate folded
  // into the explorer's `where` + resetKey, so typing would otherwise re-query
  // (dropping the explorer's caches) on every keystroke. The <Input> stays fully
  // responsive on `search`; only the debounced value drives the config/resetKey.
  const [debouncedSearch, setDebouncedSearch] = useState("");
  useEffect(() => {
    const t = setTimeout(() => setDebouncedSearch(search.trim()), 300);
    return () => clearTimeout(t);
  }, [search]);

  // Hero summary: one unfiltered grouped query (group by source + finished
  // rollup) → whole-library counts. Only when the feature is on.
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

  // The reading DomainConfig, closed over the live filters; the resetKey folds
  // the same inputs so the explorer drops its caches on any filter change.
  const explorerConfig = useMemo(
    () =>
      makeBooksExplorerConfig({
        source: sourceFilter,
        status: statusFilter,
        search: debouncedSearch,
      }),
    [sourceFilter, statusFilter, debouncedSearch],
  );
  const resetKey = `${sourceFilter}|${statusFilter}|${debouncedSearch}`;

  return (
    <Page>
      <Page.Header title="Books" />
      <Page.Body>
        <Page.Content>
          <div className="space-y-6">
            <BooksHero stats={heroStats} />

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
                {/* Controls — all fold into the DSL where: source/status as eq
                    leaves, the (debounced) search as an ILIKE OR on title/author. */}
                <div className="flex flex-wrap items-center gap-3">
                  <div className="relative min-w-[220px] flex-1">
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
                    ]}
                  />
                  <FilterSelect<StatusFilter>
                    label="Status"
                    value={statusFilter}
                    onChange={setStatusFilter}
                    options={STATUS_FILTER_OPTIONS}
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
                />
              </div>
            )}
          </div>
        </Page.Content>
      </Page.Body>
    </Page>
  );
}

export default BooksPage;
