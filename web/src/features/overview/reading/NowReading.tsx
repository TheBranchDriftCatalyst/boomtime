// NowReading — "what am I in the middle of" list. This is the one Reading tile
// that needs ROWS, not an aggregate, so it reads the existing siloed
// reading_items endpoint (api.getBooksItems / GET /api/v1/books/items) rather
// than the query DSL, then filters to status === "reading" and sorts by
// progress desc client-side. Read-only against the books API — no writes.
import { useQuery } from "@tanstack/react-query";
import { BookOpen } from "lucide-react";
import { ChartCard } from "@/components/ChartCard";
import { Spinner } from "@thebranchdriftcatalyst/catalyst-ui/ui/spinner";
import { EmptyChart } from "@/viz/d3/EmptyChart";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import type { ReadingItemDTO } from "@/types/api";

const HEIGHT = 300;

export function NowReadingTile() {
  const q = useQuery({
    queryKey: qk.readingItems(),
    queryFn: () => api.getBooksItems(),
  });

  const items: ReadingItemDTO[] = (q.data?.items ?? [])
    .filter((it) => it.status === "reading")
    .sort((a, b) => b.progressPercent - a.progressPercent);

  return (
    <ChartCard title="Now reading" subtitle="In progress">
      {q.isLoading ? (
        <div className="flex items-center justify-center" style={{ height: HEIGHT }}>
          <Spinner />
        </div>
      ) : q.isError ? (
        <div
          className="flex items-center justify-center text-sm text-muted-foreground"
          style={{ height: HEIGHT }}
        >
          Failed to load your library.
        </div>
      ) : items.length === 0 ? (
        <EmptyChart height={HEIGHT} hint="Nothing marked as currently reading." />
      ) : (
        <ul
          className="flex flex-col gap-3 overflow-y-auto pr-1"
          style={{ maxHeight: HEIGHT }}
          data-testid="now-reading-list"
        >
          {items.map((it) => {
            const pct = Math.max(0, Math.min(100, Math.round(it.progressPercent)));
            return (
              <li
                key={`${it.source}:${it.externalId}`}
                className="flex items-center gap-3"
                data-testid="now-reading-row"
              >
                {it.coverUrl ? (
                  <img
                    src={it.coverUrl}
                    alt=""
                    className="h-12 w-9 shrink-0 rounded object-cover shadow-sm ring-1 ring-border"
                    loading="lazy"
                  />
                ) : (
                  <span className="flex h-12 w-9 shrink-0 items-center justify-center rounded bg-primary/10 ring-1 ring-primary/20">
                    <BookOpen className="h-4 w-4 text-primary" />
                  </span>
                )}
                <div className="min-w-0 flex-1">
                  <div className="flex items-baseline justify-between gap-2">
                    <span className="truncate text-sm font-medium text-foreground" title={it.title}>
                      {it.title}
                    </span>
                    <span className="shrink-0 tabular-nums text-xs text-muted-foreground">
                      {pct}%
                    </span>
                  </div>
                  {it.authors && (
                    <div className="truncate text-xs text-muted-foreground" title={it.authors}>
                      {it.authors}
                    </div>
                  )}
                  <div className="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-muted/40">
                    <div
                      className="h-full rounded-full"
                      style={{
                        width: `${pct}%`,
                        background:
                          "linear-gradient(90deg, hsl(var(--primary)/0.55), hsl(var(--primary)))",
                        boxShadow: "0 0 6px hsl(var(--primary)/0.35)",
                      }}
                    />
                  </div>
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </ChartCard>
  );
}
