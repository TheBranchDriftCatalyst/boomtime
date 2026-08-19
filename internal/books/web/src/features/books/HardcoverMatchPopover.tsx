// HardcoverMatchPopover — the manual match-fixer. Click a book's Hardcover badge
// to open a popover with a live autocomplete against Hardcover's catalog; pick a
// descriptive candidate card to link (or re-link) that row. This is the
// human-in-the-loop escape hatch for the ~93% of books the automated match ladder
// can't confidently resolve.
import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Search, Loader2, BookMarked } from "lucide-react";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/popover";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import type { HardcoverCandidate, ReadingItemDTO } from "@/types/api";

// Grouped-query prefixes whose results derive from reading state — invalidate on a
// successful match so the hero/charts refetch (mirrors useSetBookCuration).
const READING_QUERY_PREFIX = ["reading-query"] as const;

export function HardcoverMatchPopover({
  item,
  trigger,
  onMatched,
}: {
  item: ReadingItemDTO;
  trigger: React.ReactNode;
  // Called with the updated (matched) row so the CELL self-updates in place — no
  // whole-table refetch.
  onMatched?: (updated: ReadingItemDTO) => void;
}) {
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [term, setTerm] = useState(
    // Seed the search with the book's own title so the first result set is useful.
    () => item.title ?? "",
  );
  const [debounced, setDebounced] = useState(term);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(term.trim()), 300);
    return () => clearTimeout(t);
  }, [term]);

  const search = useQuery({
    queryKey: qk.hardcoverSearch(debounced),
    queryFn: () => api.hardcoverSearch(debounced),
    enabled: open && debounced.trim().length >= 2,
    staleTime: 60_000,
    retry: false,
  });

  const apply = useMutation({
    mutationFn: (c: HardcoverCandidate) =>
      // Edition is resolved server-side from the book id; we pass the slug the
      // search card already carries so the deep-link works immediately.
      api.setBookManualMatch(item, {
        hardcoverBookId: c.bookId,
        slug: c.slug || undefined,
      }),
    onSuccess: (updated) => {
      // Refresh the hero counts (react-query) — cheap, off the table's render path.
      qc.invalidateQueries({ queryKey: qk.booksHero() });
      qc.invalidateQueries({ queryKey: READING_QUERY_PREFIX });
      // Self-update the CELL with the matched row (no whole-table refetch).
      onMatched?.(updated);
      setOpen(false);
    },
  });

  const candidates = search.data?.candidates ?? [];

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>{trigger}</PopoverTrigger>
      <PopoverContent align="start" className="w-96 p-0">
        <div className="border-b border-border p-2">
          <div className="relative">
            <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              autoFocus
              value={term}
              onChange={(e) => setTerm(e.target.value)}
              placeholder="Search Hardcover…"
              className="h-8 pl-7 text-sm"
            />
          </div>
        </div>
        <div className="max-h-72 overflow-y-auto p-1">
          {search.isFetching && (
            <div className="flex items-center gap-2 px-2 py-3 text-xs text-muted-foreground">
              <Loader2 className="h-3.5 w-3.5 animate-spin" /> Searching Hardcover…
            </div>
          )}
          {search.isError && (
            <div className="px-2 py-3 text-xs text-destructive">
              {(search.error as Error)?.message ?? "Search failed"}
            </div>
          )}
          {!search.isFetching &&
            !search.isError &&
            debounced.trim().length >= 2 &&
            candidates.length === 0 && (
              <div className="px-2 py-3 text-xs text-muted-foreground">
                No Hardcover matches for “{debounced}”.
              </div>
            )}
          {candidates.map((c) => (
            <button
              key={c.bookId}
              type="button"
              disabled={apply.isPending}
              onClick={() => apply.mutate(c)}
              className="flex w-full items-start gap-2 rounded-md px-2 py-1.5 text-left hover:bg-accent disabled:opacity-50"
            >
              {c.coverUrl ? (
                <img
                  src={c.coverUrl}
                  alt=""
                  className="h-16 w-11 shrink-0 rounded object-cover"
                  loading="lazy"
                />
              ) : (
                <span className="flex h-16 w-11 shrink-0 items-center justify-center rounded bg-muted">
                  <BookMarked className="h-5 w-5 text-muted-foreground" />
                </span>
              )}
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-medium">
                  {c.title}
                </span>
                <span className="block truncate text-xs text-muted-foreground">
                  {c.authors?.join(", ")}
                  {c.year ? ` · ${c.year}` : ""}
                </span>
              </span>
            </button>
          ))}
        </div>
        {apply.isError && (
          <div className="border-t border-border px-2 py-1.5 text-xs text-destructive">
            {(apply.error as Error)?.message ?? "Failed to link"}
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}
