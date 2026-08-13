// BooksExplorer — the "Explore" mode of the Books page (gaka-books). A
// heartbeats-explorer-style group-by-dimension view over READING data, powered
// by the cross-domain query DSL (runQuery).
//
// Pick a dimension (Source / Status / Series / Author / Genre) and a measure
// (Books count | Runtime), and we run ONE grouped query and render the returned
// groups as a ranked bar table — value-desc, with a trailing "Other" roll-up and
// a top-N cap (both handled server-side by the bucket policy). Single-level
// group-by only for v1; a second-level drill-down would reuse this same table
// under an expandable row (tracked as a follow-up — see the page comment).
//
// The visual language mirrors the heartbeats explorer's GroupRow (a group label
// + a proportional value bar) but in the synthwave palette the Books page
// already uses (neon gradient fill over a muted track).
import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { BarChart3, Library } from "lucide-react";
import { Card, CardContent } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { EmptyState } from "@/components/EmptyState";
import { runQuery, type GroupRow } from "@/lib/queryApi";
import { cn } from "@/lib/utils";
import {
  MEASURES,
  READING_DIMS,
  buildBooksGroupSpec,
  dimAllowed,
  formatMeasureValue,
  groupKeyLabel,
  isOtherRow,
  measureMeta,
  TOP_N,
  type BooksMeasure,
  type ReadingDim,
} from "./booksExplore";

// ── group-by control ─────────────────────────────────────────────────────────

// Single-select dimension chips + a measure segmented toggle. Dimensions not
// valid for the active measure (author under runtime) render disabled, matching
// the backend registry so we never fire a request the server would 400.
function GroupByControl({
  dim,
  measure,
  onDim,
  onMeasure,
}: {
  dim: ReadingDim;
  measure: BooksMeasure;
  onDim: (d: ReadingDim) => void;
  onMeasure: (m: BooksMeasure) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-medium text-muted-foreground">
          Group by:
        </span>
        {READING_DIMS.map(({ dim: d, label, icon: Icon }) => {
          const allowed = dimAllowed(d, measure);
          const active = d === dim;
          return (
            <button
              key={d}
              type="button"
              disabled={!allowed}
              aria-pressed={active}
              onClick={() => onDim(d)}
              title={
                allowed
                  ? `Group by ${label.toLowerCase()}`
                  : `${label} isn't available for the ${measureMeta(measure).label} measure`
              }
              className={cn(
                "inline-flex items-center gap-1.5 rounded-full border px-3 py-1 text-sm font-medium transition-colors",
                active
                  ? "border-primary/50 bg-primary/15 text-primary shadow-[0_0_12px_-2px_hsl(var(--primary)/0.5)]"
                  : "border-border bg-muted/30 text-muted-foreground hover:border-primary/30 hover:text-foreground",
                !allowed && "cursor-not-allowed opacity-40 hover:border-border hover:text-muted-foreground",
              )}
            >
              <Icon className="h-3.5 w-3.5" />
              {label}
            </button>
          );
        })}
      </div>

      <div className="flex items-center gap-2">
        <span className="text-sm font-medium text-muted-foreground">
          Measure:
        </span>
        <div className="flex items-center rounded-md border p-0.5">
          {MEASURES.map((m) => (
            <Button
              key={m.measure}
              type="button"
              variant={measure === m.measure ? "secondary" : "ghost"}
              size="sm"
              className="h-7"
              aria-pressed={measure === m.measure}
              onClick={() => onMeasure(m.measure)}
            >
              {m.measure === "books" ? "Books count" : "Runtime"}
            </Button>
          ))}
        </div>
      </div>
    </div>
  );
}

// ── ranked bar table ─────────────────────────────────────────────────────────

// One row: rank + label + proportional neon bar + formatted value. Bar width is
// relative to the largest value in the set. The "Other" roll-up reads muted +
// italic (and drops its rank) so it never looks like a real dimension value.
function GroupBarRow({
  row,
  rank,
  measure,
  max,
}: {
  row: GroupRow;
  rank: number;
  measure: BooksMeasure;
  max: number;
}) {
  const other = isOtherRow(row.key);
  const isNull = !other && row.key.trim() === "";
  const pct = max > 0 ? Math.max(2, Math.round((row.value / max) * 100)) : 0;

  return (
    <div className="flex items-center gap-3 px-3 py-2.5">
      <span className="w-5 shrink-0 text-right font-mono text-xs text-muted-foreground/70">
        {other ? "·" : rank}
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex items-center justify-between gap-3">
          <span
            className={cn(
              "truncate text-sm",
              other
                ? "italic text-muted-foreground"
                : isNull
                  ? "italic text-muted-foreground"
                  : "font-medium text-foreground",
            )}
            title={groupKeyLabel(row.key)}
          >
            {groupKeyLabel(row.key)}
          </span>
          <span className="shrink-0 font-mono text-xs tabular-nums text-muted-foreground">
            {formatMeasureValue(row.value, measure)}
          </span>
        </div>
        <div className="mt-1.5 h-1.5 w-full overflow-hidden rounded-full bg-muted">
          <div
            className={cn(
              "h-full rounded-full transition-all",
              other
                ? "bg-muted-foreground/40"
                : "bg-gradient-to-r from-primary/70 to-primary",
            )}
            style={{ width: `${pct}%` }}
          />
        </div>
      </div>
    </div>
  );
}

function GroupBarTable({
  groups,
  measure,
}: {
  groups: GroupRow[];
  measure: BooksMeasure;
}) {
  const max = useMemo(
    () => groups.reduce((m, g) => Math.max(m, g.value), 0),
    [groups],
  );
  // "Other" is always last and out of the ranked sequence.
  let rank = 0;
  return (
    <div
      data-testid="explore-groups"
      className="divide-y divide-border/60 rounded-lg border border-border"
    >
      {groups.map((g) => {
        if (!isOtherRow(g.key)) rank += 1;
        return (
          <GroupBarRow
            key={g.key}
            row={g}
            rank={rank}
            measure={measure}
            max={max}
          />
        );
      })}
    </div>
  );
}

// Skeleton bar rows while the grouped query is in flight.
function ExploreSkeleton() {
  return (
    <div
      className="divide-y divide-border/60 rounded-lg border border-border"
      data-testid="explore-skeleton"
    >
      {Array.from({ length: 8 }).map((_, i) => (
        <div key={i} className="flex items-center gap-3 px-3 py-2.5">
          <div className="h-3 w-5 shrink-0 animate-pulse rounded bg-muted" />
          <div className="flex-1 space-y-1.5">
            <div className="flex justify-between">
              <div className="h-3 w-1/3 animate-pulse rounded bg-muted" />
              <div className="h-3 w-10 animate-pulse rounded bg-muted/70" />
            </div>
            <div
              className="h-1.5 animate-pulse rounded-full bg-muted"
              style={{ width: `${90 - i * 9}%` }}
            />
          </div>
        </div>
      ))}
    </div>
  );
}

// ── explorer ─────────────────────────────────────────────────────────────────

export function BooksExplorer() {
  const [measure, setMeasure] = useState<BooksMeasure>("books");
  const [dim, setDim] = useState<ReadingDim>("author");

  // Switching measure can strand the current dimension (author under runtime);
  // fall back to "source" (valid for every measure) so we never query an
  // illegal combo.
  function pickMeasure(next: BooksMeasure) {
    setMeasure(next);
    if (!dimAllowed(dim, next)) setDim("source");
  }

  const query = useQuery({
    queryKey: ["books-explore", measure, dim],
    queryFn: () => runQuery(buildBooksGroupSpec(dim, measure)),
    staleTime: 60_000,
  });

  const groups: GroupRow[] =
    query.data?.kind === "groups" ? query.data.groups : [];

  return (
    <div className="space-y-3">
      <Card>
        <CardContent className="py-4">
          <GroupByControl
            dim={dim}
            measure={measure}
            onDim={setDim}
            onMeasure={pickMeasure}
          />
        </CardContent>
      </Card>

      {query.isLoading ? (
        <ExploreSkeleton />
      ) : query.isError ? (
        <Card>
          <CardContent className="pt-6">
            <EmptyState
              icon={BarChart3}
              title="Couldn't run that breakdown"
              description="Something went wrong grouping your library. Try another dimension or refresh the page."
            />
          </CardContent>
        </Card>
      ) : groups.length === 0 ? (
        <Card>
          <CardContent className="pt-6">
            <EmptyState
              icon={Library}
              title="Nothing to break down yet"
              description="No reading data matched this grouping. Once books are tracked, they'll rank here by your chosen dimension."
            />
          </CardContent>
        </Card>
      ) : (
        <>
          <div className="text-xs text-muted-foreground">
            {measure === "books" ? "Book count" : "Runtime"} by{" "}
            {READING_DIMS.find((d) => d.dim === dim)?.label.toLowerCase()} —
            top {TOP_N}
            {groups.some((g) => isOtherRow(g.key)) ? ", rest in Other" : ""}
          </div>
          <GroupBarTable groups={groups} measure={measure} />
        </>
      )}
    </div>
  );
}

export default BooksExplorer;
