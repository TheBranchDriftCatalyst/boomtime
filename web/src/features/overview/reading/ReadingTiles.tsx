// ReadingTiles — the query-DSL-driven tiles of the Reading dashboard. Each tile
// is exactly one (or two) `runQuery` specs bound through `useReadingQuery`,
// rendered through a shared loading / error / empty ladder. The specs mirror the
// DSL invariant that group + non-"none" granularity are mutually exclusive:
// grouped tiles use granularity "none" (the default), time-series tiles don't
// group.
import { Headphones } from "lucide-react";
import { StatCard } from "@thebranchdriftcatalyst/catalyst-ui/components/StatCard";
import { Spinner } from "@thebranchdriftcatalyst/catalyst-ui/ui/spinner";
import { ChartCard } from "@/components/ChartCard";
import { colorAt } from "@/viz/d3/color";
import type { QueryResult, QuerySpec } from "@/lib/queryApi";
import { useReadingQuery } from "./useReadingQuery";
import { fmtHoursMin, fmtMonthLabel, fmtRuntimeMin } from "./format";
import { ReadingBars, type ReadingBar } from "./ReadingBars";
import { ReadingDonut } from "./ReadingDonut";
import { ReadingTrend, type TrendSeries } from "./ReadingTrend";

// --- narrowing helpers -------------------------------------------------------
// The DSL response is a discriminated union; a tile knows which arm its spec
// yields, so these narrow-or-default helpers keep each tile terse and shield it
// from a surprise arm (defaults to the empty shape → the empty state).
const asScalar = (r?: QueryResult): number =>
  r?.kind === "scalar" ? r.scalar : 0;
const asSeries = (r?: QueryResult) =>
  r?.kind === "series" ? r.series : [];
const asGroups = (r?: QueryResult) =>
  r?.kind === "groups" ? r.groups : [];

// --- specs (exported so tests document the exact DSL each tile issues) --------
export const READING_SPECS = {
  listeningThisWeek: {
    domain: "reading",
    measure: "seconds",
    over: { granularity: "none", range: { lastN: 7 } },
  },
  listeningTrend: {
    domain: "reading",
    measure: "seconds",
    over: { granularity: "week", range: { lastN: 12 } },
  },
  codingTrend: {
    domain: "coding",
    measure: "seconds",
    over: { granularity: "week", range: { lastN: 12 } },
  },
  booksByGenre: {
    domain: "reading",
    measure: "books",
    group: "genre",
    bucket: { topN: 6, other: true },
  },
  topSeriesByRuntime: {
    domain: "reading",
    measure: "runtime",
    group: "series",
    sort: { field: "value", desc: true },
    limit: 8,
  },
  prolificGenres: {
    domain: "reading",
    measure: "books",
    group: "genre",
    having: { op: ">=", value: 3 },
  },
  finishedPerMonth: {
    domain: "reading",
    measure: "books",
    where: { kind: "leaf", dim: "status", op: "eq", values: ["read"] },
    over: { granularity: "month", range: { lastN: 12 } },
  },
} satisfies Record<string, QuerySpec>;

/** Small inline error/loading text used inside a ChartCard body. */
function ChartState({ height, children }: { height: number; children: React.ReactNode }) {
  return (
    <div
      className="flex items-center justify-center text-sm text-muted-foreground"
      style={{ height }}
    >
      {children}
    </div>
  );
}

// --- Listening this week (scalar KPI) ----------------------------------------
export function ListeningThisWeekTile() {
  const q = useReadingQuery(READING_SPECS.listeningThisWeek);
  const value = q.isLoading
    ? "…"
    : q.isError
      ? "—"
      : fmtHoursMin(asScalar(q.data));
  return (
    <StatCard
      name="Listening this week"
      value={value}
      icon={<Headphones className="h-6 w-6" />}
      accent="primary"
      subtitle="Last 7 days"
    />
  );
}

// --- Listening trend, 12 weeks (+ coding overlay) ----------------------------
export function ListeningTrendTile() {
  const reading = useReadingQuery(READING_SPECS.listeningTrend);
  const coding = useReadingQuery(READING_SPECS.codingTrend);
  const height = 260;

  const series: TrendSeries[] = [
    { name: "Listening", color: colorAt(0), points: asSeries(reading.data) },
    // Overlay coding once it resolves; a coding error just omits the overlay so
    // the reading story still renders.
    { name: "Coding", color: colorAt(4), points: asSeries(coding.data) },
  ];

  return (
    <ChartCard title="Listening trend" subtitle="Last 12 weeks · vs coding">
      {reading.isLoading ? (
        <ChartState height={height}>
          <Spinner />
        </ChartState>
      ) : reading.isError ? (
        <ChartState height={height}>Failed to load listening trend.</ChartState>
      ) : (
        <ReadingTrend
          series={series}
          height={height}
          valueFmt={fmtHoursMin}
          emptyHint="No listening activity in the last 12 weeks."
        />
      )}
    </ChartCard>
  );
}

// --- Books by genre (donut) --------------------------------------------------
export function BooksByGenreTile() {
  const q = useReadingQuery(READING_SPECS.booksByGenre);
  const height = 280;
  return (
    <ChartCard title="Books by genre" subtitle="Top 6 + other">
      {q.isLoading ? (
        <ChartState height={height}>
          <Spinner />
        </ChartState>
      ) : q.isError ? (
        <ChartState height={height}>Failed to load genres.</ChartState>
      ) : (
        <ReadingDonut
          data={asGroups(q.data)}
          height={height}
          emptyHint="No genre-tagged books yet."
        />
      )}
    </ChartCard>
  );
}

// --- Top series by runtime (bars) --------------------------------------------
export function TopSeriesByRuntimeTile() {
  const q = useReadingQuery(READING_SPECS.topSeriesByRuntime);
  const height = 280;
  const bars: ReadingBar[] = asGroups(q.data).map((g) => ({
    label: g.key,
    value: g.value,
  }));
  return (
    <ChartCard title="Top series by runtime" subtitle="Top 8">
      {q.isLoading ? (
        <ChartState height={height}>
          <Spinner />
        </ChartState>
      ) : q.isError ? (
        <ChartState height={height}>Failed to load series.</ChartState>
      ) : (
        <ReadingBars
          data={bars}
          valueFmt={fmtRuntimeMin}
          height={height}
          emptyHint="No series with recorded runtime yet."
        />
      )}
    </ChartCard>
  );
}

// --- Prolific genres, >= 3 books (bars) --------------------------------------
export function ProlificGenresTile() {
  const q = useReadingQuery(READING_SPECS.prolificGenres);
  const height = 220;
  const bars: ReadingBar[] = asGroups(q.data).map((g, i) => ({
    label: g.key,
    value: g.value,
    color: colorAt(i),
  }));
  return (
    <ChartCard title="Prolific genres" subtitle="3+ books">
      {q.isLoading ? (
        <ChartState height={height}>
          <Spinner />
        </ChartState>
      ) : q.isError ? (
        <ChartState height={height}>Failed to load genres.</ChartState>
      ) : (
        <ReadingBars
          data={bars}
          valueFmt={(v) => `${v} book${v === 1 ? "" : "s"}`}
          height={height}
          emptyHint="No genre has 3+ books yet."
        />
      )}
    </ChartCard>
  );
}

// --- Finished per month (bars over time) -------------------------------------
export function FinishedPerMonthTile() {
  const q = useReadingQuery(READING_SPECS.finishedPerMonth);
  const height = 240;
  const bars: ReadingBar[] = asSeries(q.data).map((p) => ({
    label: fmtMonthLabel(p.bucket),
    value: p.value,
    title: p.bucket,
  }));
  return (
    <ChartCard title="Finished per month" subtitle="Last 12 months">
      {q.isLoading ? (
        <ChartState height={height}>
          <Spinner />
        </ChartState>
      ) : q.isError ? (
        <ChartState height={height}>Failed to load finished books.</ChartState>
      ) : (
        <ReadingBars
          data={bars}
          valueFmt={(v) => `${v}`}
          height={height}
          emptyHint="No finished books in the last 12 months."
        />
      )}
    </ChartCard>
  );
}
