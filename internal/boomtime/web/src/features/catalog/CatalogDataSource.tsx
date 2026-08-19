// CatalogDataSource — the switchable data-source plumbing for the widget
// catalog gallery. Owns:
//
//   1. The "mine" | "sample" toggle (persisted to localStorage), via
//      <CatalogDataProvider> + useCatalogSource().
//   2. The CRUX: making every self-fetching FE-only widget (the overview-*
//      kinds, github-*, goal-*, hero-identity/labels-showcase's awards)
//      return SAMPLE data with ZERO network calls when source="sample", and
//      REAL data via the SAME hooks/endpoints the profile + overview pages
//      already use when source="mine".
//
// HOW SAMPLE SEEDING WORKS: every self-fetching hook in this codebase reads
// through react-query (useQuery({queryKey: qk.xxx(...), queryFn: ...})).
// Rather than mocking fetch or forking every hook, this file pre-populates a
// DEDICATED, isolated QueryClient (getSampleQueryClient()) with
// `setQueryData` for every query key those hooks can possibly compute under
// a FIXED sample OverviewDataContext value (fixed tr/timelineHours/space —
// see buildSampleOverviewValue), and sets staleTime/gcTime to Infinity with
// every refetch trigger off. When a component mounts inside
// <QueryClientProvider client={getSampleQueryClient()}>, react-query finds
// the data already cached and NEVER calls the real queryFn — no network,
// full type-fidelity (the hooks run their normal derivation logic over real
// sample payloads, not a hand-mocked component tree). CatalogWidgetRenderer
// nests this QueryClientProvider (+ a matching OverviewDataProvider) around
// the sample-mode render tree; "mine" mode uses the app's ambient
// QueryClientProvider untouched, with a real OverviewDataProvider value
// built from `rangeDays`.
import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";
import { QueryClient } from "@tanstack/react-query";
import { qk } from "@/lib/queryKeys";
import type { QueryResult, QuerySpec } from "@/lib/queryApi";
import { READING_SPECS } from "@/features/overview/reading/ReadingTiles";
import {
  DEFAULT_RANGE_KEY,
  READING_RANGE_PRESETS,
  readingSpecsForRange,
} from "@/features/overview/reading/readingRange";
import { loadStored, saveStored } from "@/lib/persist";
import { DEFAULT_TIME_LIMIT, TIMELINE_HOUR_OPTIONS } from "@/lib/config";
import type { OverviewDataContextValue } from "@/features/overview/OverviewDataContext";
import type { TimeRangeControls } from "@/hooks/useTimeRange";
import type {
  MomentumPayload,
  PublicDashboardPayload,
  PunchcardPayload,
  SessionsPayload,
  StatsPayload,
} from "@/types/stats";
import {
  SAMPLE_AI_ACTIVITY,
  SAMPLE_AWARDS,
  SAMPLE_AWARD_STREAKS,
  SAMPLE_DASHBOARD_PAYLOAD,
  SAMPLE_END_ISO,
  SAMPLE_GITHUB_STATS,
  SAMPLE_GOALS,
  SAMPLE_GOALS_PROGRESS,
  SAMPLE_HEALTH_ACTIVITY,
  SAMPLE_LOC,
  SAMPLE_MOMENTUM,
  SAMPLE_PUBLIC_CONFIG,
  SAMPLE_SESSIONS,
  SAMPLE_START_ISO,
  SAMPLE_STATS,
  SAMPLE_TIMELINE_BY_HOURS,
  SAMPLE_TIMELINE_HOURS,
  SAMPLE_TIME_LIMIT,
  SAMPLE_USERNAME,
} from "./sampleData";

// ---------------------------------------------------------------------------
// Source toggle
// ---------------------------------------------------------------------------

export type CatalogSource = "mine" | "sample";

const STORAGE_KEY = "boomtime.catalog.source";

export interface CatalogDataContextValue {
  source: CatalogSource;
  setSource: (s: CatalogSource) => void;
}

const CatalogDataContext = createContext<CatalogDataContextValue | null>(null);

/** Wrap the /catalog page in this once. Persists the mine/sample choice to
 * localStorage; defaults to "sample" so a fresh/no-data user (and, later, an
 * unauth visitor) sees populated widgets immediately instead of a wall of
 * empty states. */
export function CatalogDataProvider({ children }: { children: ReactNode }) {
  const [source, setSourceState] = useState<CatalogSource>(() =>
    loadStored<CatalogSource>(STORAGE_KEY, "sample"),
  );
  const setSource = useCallback((s: CatalogSource) => {
    setSourceState(s);
    saveStored(STORAGE_KEY, s);
  }, []);
  const value = useMemo(() => ({ source, setSource }), [source, setSource]);
  return <CatalogDataContext.Provider value={value}>{children}</CatalogDataContext.Provider>;
}

export function useCatalogSource(): CatalogDataContextValue {
  const ctx = useContext(CatalogDataContext);
  if (!ctx) {
    throw new Error("useCatalogSource must be used within a CatalogDataProvider");
  }
  return ctx;
}

// ---------------------------------------------------------------------------
// Shared payload shape both source modes build. `momentum`/`sessions` don't
// live on PublicDashboardPayload (the public mirror deliberately omits
// them — see SpecRenderData's doc comment in SpecRenderer.tsx) but ARE
// needed in-page for the "momentum" and "deep-work" both-target kinds, so
// this is the wider type CatalogWidgetRenderer builds and feeds to
// WidgetRenderer/SpecRenderer — a plain superset, structurally assignable
// everywhere a PublicDashboardPayload or SpecRenderData is expected.
// ---------------------------------------------------------------------------
export type CatalogPayload = PublicDashboardPayload & {
  momentum?: MomentumPayload;
  sessions?: SessionsPayload;
};

export const EMPTY_PUNCHCARD: PunchcardPayload = { cells: [], maxSeconds: 0, totalSeconds: 0 };

// ---------------------------------------------------------------------------
// "mine" mode helpers
// ---------------------------------------------------------------------------

/** A read-only TimeRangeControls built directly from `rangeDays`, ending
 * "now" — deliberately NOT useTimeRange() (which reads/writes the URL +
 * localStorage): the catalog gallery wants a simple day-count knob, not to
 * hijack the page's query string every time a card mounts. Setters are
 * no-ops; nothing in the render path calls them (the catalog is read-only —
 * the parent page owns any interactive range picker). */
export function buildTimeRangeControls(rangeDays: number): TimeRangeControls {
  const end = new Date();
  const start = new Date(end.getTime() - Math.max(1, rangeDays) * 86_400_000);
  return {
    start,
    end,
    numDays: rangeDays,
    timeLimit: DEFAULT_TIME_LIMIT,
    startISO: start.toISOString(),
    endISO: end.toISOString(),
    setDaysFromToday: () => {},
    setRange: () => {},
    setTimeLimit: () => {},
  };
}

/** OverviewDataContext value for "mine" mode — real tr, no Space scoping
 * (the catalog gallery is a personal, unscoped view). `timelineHours` is
 * threaded from live state so the Overview-timeline widget's hour picker
 * stays interactive. */
export function buildRealOverviewValue(
  rangeDays: number,
  timelineHours: number,
  setTimelineHours: (n: number) => void,
): OverviewDataContextValue {
  return {
    tr: buildTimeRangeControls(rangeDays),
    timelineHours,
    setTimelineHours,
    space: undefined,
  };
}

/** Compose the CatalogPayload for "mine" mode from the SAME hooks the
 * Overview page uses (useOverviewStats / useOverviewPunchcard /
 * useOverviewMomentum / useOverviewSessions — see CatalogWidgetRenderer.tsx)
 * plus the caller's own username. Mirrors OverviewWidgetRenderer.tsx's
 * `specDataFromStats` helper, widened to a full CatalogPayload. */
export function buildRealCatalogPayload(
  username: string,
  stats: StatsPayload | undefined,
  punchcard: PunchcardPayload | undefined,
  momentum: MomentumPayload | undefined,
  sessions: SessionsPayload | undefined,
): CatalogPayload {
  return {
    username: username || "you",
    startDate: stats?.startDate ?? new Date(0).toISOString(),
    endDate: stats?.endDate ?? new Date(0).toISOString(),
    totalSeconds: stats?.totalSeconds ?? 0,
    dailyAvg: stats?.dailyAvg ?? 0,
    dailyTotal: stats?.dailyTotal ?? [],
    projects: stats?.projects ?? [],
    languages: stats?.languages ?? [],
    editors: stats?.editors ?? [],
    platforms: stats?.platforms ?? [],
    categories: stats?.categories ?? [],
    punchcard: punchcard ?? EMPTY_PUNCHCARD,
    momentum,
    sessions,
  };
}

// ---------------------------------------------------------------------------
// "sample" mode helpers
// ---------------------------------------------------------------------------

/** The fixed CatalogPayload for sample mode — the full 90-day fixture plus
 * momentum/sessions so momentum/deep-work render populated too. Static; no
 * hooks, no windowing by rangeDays (see CatalogWidgetRenderer's file doc for
 * why sample mode intentionally ignores rangeDays). */
export const SAMPLE_CATALOG_PAYLOAD: CatalogPayload = {
  ...SAMPLE_DASHBOARD_PAYLOAD,
  momentum: SAMPLE_MOMENTUM,
  sessions: SAMPLE_SESSIONS,
};

/** OverviewDataContext value for sample mode — a FIXED tr matching the exact
 * start/end the sample fixtures were generated against (see sampleData.ts's
 * SAMPLE_START_ISO/SAMPLE_END_ISO), so any component that self-fetches
 * through it computes the SAME query keys this file seeded. */
export function buildSampleOverviewValue(
  timelineHours: number,
  setTimelineHours: (n: number) => void,
): OverviewDataContextValue {
  return {
    tr: {
      start: new Date(SAMPLE_START_ISO),
      end: new Date(SAMPLE_END_ISO),
      numDays: SAMPLE_DASHBOARD_PAYLOAD.dailyTotal.length,
      timeLimit: SAMPLE_TIME_LIMIT,
      startISO: SAMPLE_START_ISO,
      endISO: SAMPLE_END_ISO,
      setDaysFromToday: () => {},
      setRange: () => {},
      setTimeLimit: () => {},
    },
    timelineHours,
    setTimelineHours,
    space: undefined,
  };
}

let sampleQueryClientSingleton: QueryClient | null = null;

/** Lazily builds (once) and returns the shared, pre-seeded sample
 * QueryClient. A module-level singleton: sample data is static, so every
 * <CatalogWidgetRenderer source="sample"> instance on the page shares ONE
 * seeded cache instead of re-seeding per card. staleTime/gcTime: Infinity +
 * every refetch trigger off means a cache HIT never re-invokes queryFn — the
 * only way a network call fires in sample mode is a query key this file
 * failed to seed (a bug to fix, not a runtime fallback), which MSW's
 * onUnhandledRequest:"error" in tests will surface loudly. */
export function getSampleQueryClient(): QueryClient {
  if (sampleQueryClientSingleton) return sampleQueryClientSingleton;
  const qc = new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: Infinity,
        gcTime: Infinity,
        retry: false,
        refetchOnMount: false,
        refetchOnWindowFocus: false,
        refetchOnReconnect: false,
      },
    },
  });
  seedSampleQueryClient(qc);
  sampleQueryClientSingleton = qc;
  return qc;
}

function seedSampleQueryClient(qc: QueryClient): void {
  const space = undefined;

  // Overview self-fetch hooks (overviewWidgets.ts) — keyed against the FIXED
  // sample tr built by buildSampleOverviewValue.
  qc.setQueryData(qk.stats(SAMPLE_START_ISO, SAMPLE_END_ISO, SAMPLE_TIME_LIMIT, space), SAMPLE_STATS);
  qc.setQueryData(qk.punchcard(SAMPLE_START_ISO, SAMPLE_END_ISO, SAMPLE_TIME_LIMIT, space), SAMPLE_DASHBOARD_PAYLOAD.punchcard);
  qc.setQueryData(qk.sessions(SAMPLE_START_ISO, SAMPLE_END_ISO, SAMPLE_TIME_LIMIT, space), SAMPLE_SESSIONS);
  qc.setQueryData(qk.momentum(SAMPLE_START_ISO, SAMPLE_END_ISO, space), SAMPLE_MOMENTUM);
  qc.setQueryData(qk.loc(SAMPLE_START_ISO, SAMPLE_END_ISO, space), SAMPLE_LOC);
  qc.setQueryData(qk.aiActivity(SAMPLE_START_ISO, SAMPLE_END_ISO), SAMPLE_AI_ACTIVITY);
  qc.setQueryData(qk.healthActivity(SAMPLE_START_ISO, SAMPLE_END_ISO), SAMPLE_HEALTH_ACTIVITY);
  // Every TIMELINE_HOUR_OPTIONS entry, so the "Recent timeline" widget's
  // hour-picker dropdown never has to fall back to a live fetch.
  for (const hours of TIMELINE_HOUR_OPTIONS) {
    qc.setQueryData(
      qk.timeline(hours, SAMPLE_TIME_LIMIT, space),
      SAMPLE_TIMELINE_BY_HOURS[hours] ?? SAMPLE_TIMELINE_BY_HOURS[SAMPLE_TIMELINE_HOURS],
    );
  }

  // Goals (useGoalsQuery / useAllGoalProgress — goal-* "both" kinds).
  qc.setQueryData(qk.goals(), SAMPLE_GOALS);
  qc.setQueryData(qk.goalsProgress(), SAMPLE_GOALS_PROGRESS);

  // Awards (useAwards / useAwardStreaks — hero-identity + labels-showcase).
  // "own" is the branch these hooks take on any route without a :slug param
  // (see useAwards.ts's route-sniffing doc comment); the catalog page is
  // assumed to be a plain (non-:slug) route. If the parent mounts the
  // catalog under a `/catalog/:slug`-shaped path this would need re-seeding
  // under the "public" key instead — flagged in the handoff report.
  qc.setQueryData(qk.awards("own"), SAMPLE_AWARDS);
  qc.setQueryData(qk.awardStreaks(), SAMPLE_AWARD_STREAKS);

  // GitHub (useGithubStatsWidget + GithubCard — github-* kinds). Seed the
  // public config FIRST conceptually (both are independent cache entries,
  // order doesn't matter for setQueryData, but the enabled-gate reads config
  // before githubStats): github_connect_enabled:true so the gated widgets
  // render their charts instead of a "Connect GitHub" CTA / self-hide.
  qc.setQueryData(qk.publicConfig(), SAMPLE_PUBLIC_CONFIG);
  qc.setQueryData(qk.githubStats(), SAMPLE_GITHUB_STATS);
  qc.setQueryData(qk.publicGithubStats(SAMPLE_USERNAME), SAMPLE_GITHUB_STATS);

  // Reading domain (gaka-qcxg) — the reading-* catalog kinds self-fetch via
  // useReadingQuery (key ["reading-query", spec], see useReadingQuery.ts).
  // Seed the exact specs each tile issues so sample mode is zero-network like
  // every other kind. The two grouped tiles use the module-level READING_SPECS
  // verbatim; the three windowed tiles derive their spec from the DEFAULT range
  // preset (12w) — the same one useReadingRange() reports on a clean load — so
  // the seeded and rendered specs are structurally identical (react-query
  // hashes them by value).
  seedReadingSample(qc);
}

/** ["reading-query", spec] is the useReadingQuery cache key; seeding it makes a
 * reading tile render populated without a runQuery network call. */
function seedReading(qc: QueryClient, spec: QuerySpec, result: QueryResult): void {
  qc.setQueryData(["reading-query", spec], result);
}

function seedReadingSample(qc: QueryClient): void {
  const preset =
    READING_RANGE_PRESETS.find((p) => p.key === DEFAULT_RANGE_KEY) ??
    READING_RANGE_PRESETS[0];
  const windowed = readingSpecsForRange(preset);

  // Scalar KPI — total listening seconds in range (~42h 30m).
  seedReading(qc, windowed.listeningInRange, {
    kind: "scalar",
    scalar: 42 * 3600 + 30 * 60,
  });

  // Listening + coding trend lines (weekly buckets over the 12w window).
  const weeks = Array.from({ length: 12 }, (_, i) => {
    const d = new Date(Date.UTC(2026, 4, 4)); // a Monday
    d.setUTCDate(d.getUTCDate() + i * 7);
    return d.toISOString();
  });
  const listenSecs = [5400, 7200, 3600, 9000, 6300, 8100, 4500, 10800, 7200, 5400, 9900, 6300];
  const codeSecs = [12000, 9000, 15000, 8000, 11000, 13000, 7000, 16000, 10000, 12000, 9000, 14000];
  seedReading(qc, windowed.listeningTrend, {
    kind: "series",
    series: weeks.map((bucket, i) => ({ bucket, value: listenSecs[i] })),
  });
  seedReading(qc, windowed.codingTrend, {
    kind: "series",
    series: weeks.map((bucket, i) => ({ bucket, value: codeSecs[i] })),
  });

  // Finished-per-month bars (monthly buckets over the finished window).
  const finished = [3, 2, 4, 1, 5, 2];
  seedReading(qc, windowed.finishedPerMonth, {
    kind: "series",
    series: finished.map((value, i) => ({
      bucket: new Date(Date.UTC(2026, 1 + i, 1)).toISOString(),
      value,
    })),
  });

  // Books-by-genre donut (top 6 + other).
  seedReading(qc, READING_SPECS.booksByGenre, {
    kind: "groups",
    groups: [
      { key: "Science Fiction", value: 14 },
      { key: "Fantasy", value: 11 },
      { key: "History", value: 7 },
      { key: "Biography", value: 5 },
      { key: "Mystery", value: 4 },
      { key: "Science", value: 3 },
      { key: "Other", value: 9 },
    ],
  });

  // Top-series-by-runtime bars (runtime minutes).
  seedReading(qc, READING_SPECS.topSeriesByRuntime, {
    kind: "groups",
    groups: [
      { key: "The Expanse", value: 3120 },
      { key: "Stormlight Archive", value: 2760 },
      { key: "Dune", value: 1980 },
      { key: "The Wheel of Time", value: 1740 },
      { key: "Foundation", value: 1260 },
      { key: "Culture", value: 900 },
    ],
  });
}
