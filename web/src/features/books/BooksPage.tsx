// Books — top-level page (gaka-books). A read-only v0 window onto the siloed
// reading_items table: every tracked book + audiobook (Audible today, Kindle
// later), fused into one synthwave dashboard. No edit/delete here — the Amazon
// connect + sync/backfill controls live in Settings › Connections.
//
// All data comes from ONE fetch (getReadingItems, all sources); search / source
// / status filters and the sort are entirely client-side over that list.
import { useMemo, useState } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import {
  BarChart3,
  BookMarked,
  BookOpen,
  Headphones,
  Library,
  Search,
  Star,
  Table2,
} from "lucide-react";
import { Card, CardContent } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Page } from "@/layout/Page";
import { EmptyState } from "@/components/EmptyState";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { usePublicConfig } from "@/lib/usePublicConfig";
import { cn } from "@/lib/utils";
import { BooksExplorer } from "@/features/books/BooksExplorer";
import type { ReadingItemDTO } from "@/types/meta";

// ── small helpers ────────────────────────────────────────────────────────────

type SourceFilter = "all" | "audible" | "kindle";
type StatusFilter = "all" | "reading" | "finished" | "want";
type SortKey = "synced" | "title" | "finished";
// Table = the flat reading_items list; Explore = group-by-dimension breakdowns
// via the query DSL (see BooksExplorer).
type ViewMode = "table" | "explore";

const fmtDate = (iso?: string): string => {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime())
    ? "—"
    : d.toLocaleDateString(undefined, {
        year: "numeric",
        month: "short",
        day: "numeric",
      });
};

// The source drives the badge glyph + palette. Audible = amber/headphones,
// Kindle = sky/book. Anything else falls back to a neutral chip.
function SourceBadge({ source }: { source: string }) {
  const s = source.toLowerCase();
  if (s === "audible") {
    return (
      <span className="inline-flex items-center gap-1.5 rounded-full border border-amber-500/40 bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium text-amber-500 dark:text-amber-400">
        <Headphones className="h-3 w-3" />
        Audible
      </span>
    );
  }
  if (s === "kindle") {
    return (
      <span className="inline-flex items-center gap-1.5 rounded-full border border-sky-500/40 bg-sky-500/10 px-2 py-0.5 text-[11px] font-medium text-sky-600 dark:text-sky-400">
        <BookOpen className="h-3 w-3" />
        Kindle
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1.5 rounded-full border border-border bg-muted/40 px-2 py-0.5 text-[11px] font-medium text-muted-foreground">
      {source || "—"}
    </span>
  );
}

// Status pill: want / reading / read / paused / dnf. Each gets a distinct hue.
const STATUS_META: Record<string, { label: string; className: string }> = {
  reading: {
    label: "Reading",
    className: "border-primary/40 bg-primary/10 text-primary",
  },
  read: {
    label: "Finished",
    className:
      "border-emerald-500/40 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400",
  },
  want: {
    label: "Want",
    className:
      "border-violet-500/40 bg-violet-500/10 text-violet-600 dark:text-violet-400",
  },
  paused: {
    label: "Paused",
    className:
      "border-amber-500/40 bg-amber-500/10 text-amber-600 dark:text-amber-400",
  },
  dnf: {
    label: "DNF",
    className:
      "border-rose-500/40 bg-rose-500/10 text-rose-600 dark:text-rose-400",
  },
};

function StatusPill({ status, finished }: { status: string; finished: boolean }) {
  const key = status.toLowerCase();
  const meta =
    STATUS_META[key] ?? (finished ? STATUS_META.read : STATUS_META.reading);
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full border px-2 py-0.5 text-[11px] font-medium",
        meta.className,
      )}
    >
      {meta.label}
    </span>
  );
}

// Slim progress bar — a neon fill over a track. Clamped 0..100.
function ProgressBar({ pct }: { pct: number }) {
  const v = Math.max(0, Math.min(100, Math.round(pct)));
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 w-20 overflow-hidden rounded-full bg-muted">
        <div
          className="h-full rounded-full bg-gradient-to-r from-primary/70 to-primary transition-all"
          style={{ width: `${v}%` }}
        />
      </div>
      <span className="w-8 shrink-0 text-right font-mono text-[11px] text-muted-foreground">
        {v}%
      </span>
    </div>
  );
}

// Rating cell: the user's own rating wins (accent star). Otherwise the Goodreads
// community average is shown muted with a "GR" hint so the two never blur.
function RatingCell({ item }: { item: ReadingItemDTO }) {
  if (typeof item.rating === "number" && item.rating > 0) {
    return (
      <span className="inline-flex items-center gap-1 text-xs font-medium text-foreground">
        <Star className="h-3.5 w-3.5 fill-primary text-primary" />
        {item.rating.toFixed(1)}
      </span>
    );
  }
  if (typeof item.goodreadsRating === "number" && item.goodreadsRating > 0) {
    return (
      <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
        <Star className="h-3.5 w-3.5 text-muted-foreground/60" />
        {item.goodreadsRating.toFixed(2)}
        <span className="rounded bg-muted px-1 py-px font-mono text-[9px] uppercase tracking-wide text-muted-foreground/70">
          GR
        </span>
      </span>
    );
  }
  return <span className="text-muted-foreground/50">—</span>;
}

// Cover thumbnail with a graceful synthwave fallback tile when no URL exists
// (or the image 404s).
function Cover({ item }: { item: ReadingItemDTO }) {
  const [broken, setBroken] = useState(false);
  const show = item.coverUrl && !broken;
  return (
    <div className="relative h-14 w-10 shrink-0 overflow-hidden rounded-sm ring-1 ring-border">
      {show ? (
        <img
          src={item.coverUrl}
          alt=""
          loading="lazy"
          onError={() => setBroken(true)}
          className="h-full w-full object-cover"
        />
      ) : (
        <div className="flex h-full w-full items-center justify-center bg-gradient-to-br from-primary/15 via-muted to-background">
          <BookMarked className="h-4 w-4 text-primary/50" />
        </div>
      )}
    </div>
  );
}

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

function BooksHero({ items }: { items: ReadingItemDTO[] }) {
  const total = items.length;
  const finished = items.filter((i) => i.finished).length;
  const audible = items.filter((i) => i.source.toLowerCase() === "audible").length;
  const kindle = items.filter((i) => i.source.toLowerCase() === "kindle").length;

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
        <h2 className="mt-2 text-2xl font-semibold tracking-tight">Books & Audiobooks</h2>
        <p className="mt-1.5 max-w-xl text-sm text-muted-foreground">
          Everything boomtime tracks from your linked Amazon account — Audible listens
          today, Kindle reads next — fused into one library view.
        </p>
        <div className="mt-4 flex flex-wrap gap-2.5">
          <StatChip icon={Library} label="Tracked" value={total} />
          <StatChip icon={BookMarked} label="Finished" value={finished} />
          <StatChip icon={Headphones} label="Audible" value={audible} />
          <StatChip icon={BookOpen} label="Kindle" value={kindle} />
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

// ── table ────────────────────────────────────────────────────────────────────

function BooksTable({ items }: { items: ReadingItemDTO[] }) {
  return (
    <div className="overflow-x-auto rounded-lg border border-border">
      <table className="w-full min-w-[880px] border-collapse text-sm">
        <thead>
          <tr className="border-b border-border bg-muted/30 text-left">
            <th className="px-3 py-2.5 font-medium" colSpan={2}>
              Title
            </th>
            <th className="px-3 py-2.5 font-medium">Author</th>
            <th className="px-3 py-2.5 font-medium">Source</th>
            <th className="px-3 py-2.5 font-medium">Status</th>
            <th className="px-3 py-2.5 font-medium">Progress</th>
            <th className="px-3 py-2.5 font-medium">Finished</th>
            <th className="px-3 py-2.5 text-right font-medium">Rating</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => {
            const isAudio = item.source.toLowerCase() === "audible";
            return (
              <tr
                key={`${item.source}:${item.externalId}`}
                className="border-b border-border/60 last:border-0 hover:bg-muted/20"
              >
                <td className="py-2.5 pl-3 pr-0 align-middle">
                  <Cover item={item} />
                </td>
                <td className="max-w-[320px] py-2.5 pl-3 pr-3 align-middle">
                  <div className="truncate font-medium text-foreground" title={item.title}>
                    {item.title}
                  </div>
                  {item.subtitle && (
                    <div
                      className="truncate text-xs text-muted-foreground"
                      title={item.subtitle}
                    >
                      {item.subtitle}
                    </div>
                  )}
                  {item.series && (
                    <div className="truncate text-[11px] italic text-primary/70">
                      {item.series}
                    </div>
                  )}
                </td>
                <td className="max-w-[200px] px-3 py-2.5 align-middle">
                  <div className="truncate text-sm" title={item.authors}>
                    {item.authors || "—"}
                  </div>
                  {isAudio && item.narrators && (
                    <div
                      className="truncate text-[11px] text-muted-foreground"
                      title={item.narrators}
                    >
                      Narrated by {item.narrators}
                    </div>
                  )}
                </td>
                <td className="px-3 py-2.5 align-middle">
                  <SourceBadge source={item.source} />
                </td>
                <td className="px-3 py-2.5 align-middle">
                  <StatusPill status={item.status} finished={item.finished} />
                </td>
                <td className="px-3 py-2.5 align-middle">
                  <ProgressBar pct={item.progressPercent} />
                </td>
                <td className="whitespace-nowrap px-3 py-2.5 align-middle text-sm text-muted-foreground">
                  {fmtDate(item.finishedAt)}
                </td>
                <td className="px-3 py-2.5 text-right align-middle">
                  <RatingCell item={item} />
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

// Skeleton rows while the (single) fetch is in flight — previews the eventual
// table shape rather than a bare spinner.
function TableSkeleton() {
  return (
    <div className="overflow-hidden rounded-lg border border-border">
      <div className="border-b border-border bg-muted/30 px-3 py-2.5" />
      {Array.from({ length: 8 }).map((_, i) => (
        <div
          key={i}
          className="flex items-center gap-3 border-b border-border/60 px-3 py-2.5 last:border-0"
        >
          <div className="h-14 w-10 shrink-0 animate-pulse rounded-sm bg-muted" />
          <div className="flex-1 space-y-1.5">
            <div className="h-3.5 w-1/3 animate-pulse rounded bg-muted" />
            <div className="h-3 w-1/4 animate-pulse rounded bg-muted/70" />
          </div>
          <div className="h-5 w-16 animate-pulse rounded-full bg-muted" />
          <div className="h-1.5 w-24 animate-pulse rounded-full bg-muted" />
        </div>
      ))}
    </div>
  );
}

// ── page ─────────────────────────────────────────────────────────────────────

export function BooksPage() {
  const { config } = usePublicConfig();
  const booksEnabled = config.books_enabled;

  const [mode, setMode] = useState<ViewMode>("table");
  const [search, setSearch] = useState("");
  const [sourceFilter, setSourceFilter] = useState<SourceFilter>("all");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [sort, setSort] = useState<SortKey>("synced");

  const query = useQuery({
    queryKey: qk.readingItems(),
    queryFn: () => api.getBooksItems(),
    // Only fetch when the feature is on — otherwise the endpoint 404s / is inert.
    enabled: booksEnabled,
    staleTime: 60_000,
  });

  const allItems = query.data?.items ?? [];

  const visible = useMemo(() => {
    const q = search.trim().toLowerCase();
    let out = allItems.filter((it) => {
      if (q) {
        const hay = `${it.title} ${it.authors}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      if (sourceFilter !== "all" && it.source.toLowerCase() !== sourceFilter) {
        return false;
      }
      if (statusFilter === "reading" && it.status.toLowerCase() !== "reading") return false;
      if (statusFilter === "finished" && !it.finished) return false;
      if (statusFilter === "want" && it.status.toLowerCase() !== "want") return false;
      return true;
    });

    out = [...out].sort((a, b) => {
      if (sort === "title") return a.title.localeCompare(b.title);
      if (sort === "finished") {
        // Recently finished first; items without a finish date sink to the end.
        const av = a.finishedAt ? Date.parse(a.finishedAt) : -Infinity;
        const bv = b.finishedAt ? Date.parse(b.finishedAt) : -Infinity;
        return bv - av;
      }
      // Recently synced (default).
      return Date.parse(b.syncedAt) - Date.parse(a.syncedAt);
    });
    return out;
  }, [allItems, search, sourceFilter, statusFilter, sort]);

  return (
    <Page>
      <Page.Header title="Books">
        {booksEnabled && (
          <div className="flex items-center rounded-md border p-0.5">
            <Button
              variant={mode === "table" ? "secondary" : "ghost"}
              size="sm"
              className="h-7"
              aria-pressed={mode === "table"}
              onClick={() => setMode("table")}
            >
              <Table2 className="h-4 w-4" />
              Table
            </Button>
            <Button
              variant={mode === "explore" ? "secondary" : "ghost"}
              size="sm"
              className="h-7"
              aria-pressed={mode === "explore"}
              onClick={() => setMode("explore")}
            >
              <BarChart3 className="h-4 w-4" />
              Explore
            </Button>
          </div>
        )}
      </Page.Header>
      <Page.Body>
        <Page.Content>
          <div className="space-y-6">
            <BooksHero items={allItems} />

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
            ) : mode === "explore" ? (
              <BooksExplorer />
            ) : query.isError ? (
              <Card>
                <CardContent className="pt-6">
                  <EmptyState
                    icon={Library}
                    title="Couldn't load your library"
                    description="Something went wrong fetching your tracked books. Try refreshing the page."
                  />
                </CardContent>
              </Card>
            ) : query.isLoading ? (
              <TableSkeleton />
            ) : allItems.length === 0 ? (
              <Card>
                <CardContent className="pt-6">
                  <EmptyState
                    icon={BookMarked}
                    title="No books tracked yet"
                    description="Connect your Amazon account and run a backfill to import your Audible library and reading history."
                    action={
                      <Button asChild size="sm">
                        <Link to="/app/settings?tab=connections">
                          <Library className="mr-1.5 h-4 w-4" />
                          Connect Amazon
                        </Link>
                      </Button>
                    }
                  />
                </CardContent>
              </Card>
            ) : (
              <div className="space-y-3">
                {/* Controls */}
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
                    options={[
                      { value: "all", label: "All" },
                      { value: "reading", label: "Reading" },
                      { value: "finished", label: "Finished" },
                      { value: "want", label: "Want" },
                    ]}
                  />
                  <FilterSelect<SortKey>
                    label="Sort"
                    value={sort}
                    onChange={setSort}
                    options={[
                      { value: "synced", label: "Recently synced" },
                      { value: "title", label: "Title" },
                      { value: "finished", label: "Recently finished" },
                    ]}
                  />
                </div>

                <div className="text-xs text-muted-foreground">
                  Showing {visible.length} of {allItems.length}
                </div>

                {visible.length === 0 ? (
                  <Card>
                    <CardContent className="pt-6">
                      <EmptyState
                        icon={Search}
                        title="No matches"
                        description="No tracked books match your current search and filters."
                      />
                    </CardContent>
                  </Card>
                ) : (
                  <BooksTable items={visible} />
                )}
              </div>
            )}
          </div>
        </Page.Content>
      </Page.Body>
    </Page>
  );
}

export default BooksPage;
