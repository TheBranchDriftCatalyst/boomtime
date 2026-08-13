// readingRange — the selected time window for the Reading dashboard (gaka-h2pg).
//
// A module-level store (same pattern as the public-profile `profileRange` and
// the feature-flags store) so the segmented control in the dashboard header and
// each windowed tile share ONE selection without prop-drilling or a provider —
// every tile fetches its own QuerySpec, so they can't take the value as a prop
// from a common parent without lifting all of them.
//
// Unlike profileRange (deliberately transient), the reading window is persisted
// to localStorage: it's a "how do I like to look at my library" preference, so
// it should survive a reload. Persistence is best-effort — a blocked or
// unavailable storage just falls back to the in-memory default.
import { useCallback, useSyncExternalStore } from "react";
import type { Granularity, QuerySpec } from "@/lib/queryApi";

/**
 * One selectable window. Each preset fully parameterizes the three windowed
 * tiles so a selection is coherent across granularities:
 *   - `days`     → the scalar "Listening in range" tile (granularity "none")
 *   - `trend`    → the listening/coding trend line (week vs month buckets)
 *   - `finished` → the "Finished per <bucket>" bars
 */
export interface ReadingRangePreset {
  key: string;
  label: string;
  /** Scalar window length in days (the `lastN` for granularity "none"). */
  days: number;
  /** Subtitle for the scalar KPI tile, e.g. "Last 12 weeks". */
  scalarSubtitle: string;
  /** Subtitle for the trend tile, e.g. "Last 12 weeks · vs coding". */
  trendSubtitle: string;
  /** Subtitle for the finished-per-bucket tile, e.g. "Last 6 months". */
  finishedSubtitle: string;
  trend: { granularity: Granularity; lastN: number };
  finished: { granularity: Granularity; lastN: number };
}

export const READING_RANGE_PRESETS: ReadingRangePreset[] = [
  {
    key: "4w",
    label: "4W",
    days: 28,
    scalarSubtitle: "Last 4 weeks",
    trendSubtitle: "Last 4 weeks · vs coding",
    finishedSubtitle: "Last 3 months",
    trend: { granularity: "week", lastN: 4 },
    finished: { granularity: "month", lastN: 3 },
  },
  {
    key: "12w",
    label: "12W",
    days: 84,
    scalarSubtitle: "Last 12 weeks",
    trendSubtitle: "Last 12 weeks · vs coding",
    finishedSubtitle: "Last 6 months",
    trend: { granularity: "week", lastN: 12 },
    finished: { granularity: "month", lastN: 6 },
  },
  {
    key: "6mo",
    label: "6M",
    days: 182,
    scalarSubtitle: "Last 6 months",
    trendSubtitle: "Last 6 months · vs coding",
    finishedSubtitle: "Last 6 months",
    trend: { granularity: "month", lastN: 6 },
    finished: { granularity: "month", lastN: 6 },
  },
  {
    key: "12mo",
    label: "12M",
    days: 365,
    scalarSubtitle: "Last 12 months",
    trendSubtitle: "Last 12 months · vs coding",
    finishedSubtitle: "Last 12 months",
    trend: { granularity: "month", lastN: 12 },
    finished: { granularity: "month", lastN: 12 },
  },
];

export const DEFAULT_RANGE_KEY = "12w";

const STORAGE_KEY = "boomtime.reading.range";

function presetByKey(key: string): ReadingRangePreset {
  return (
    READING_RANGE_PRESETS.find((p) => p.key === key) ??
    READING_RANGE_PRESETS.find((p) => p.key === DEFAULT_RANGE_KEY) ??
    READING_RANGE_PRESETS[0]
  );
}

function loadInitialKey(): string {
  if (typeof window === "undefined") return DEFAULT_RANGE_KEY;
  try {
    const saved = window.localStorage.getItem(STORAGE_KEY);
    if (saved && READING_RANGE_PRESETS.some((p) => p.key === saved)) return saved;
  } catch {
    /* storage blocked — fall through to default */
  }
  return DEFAULT_RANGE_KEY;
}

let currentKey = loadInitialKey();
const listeners = new Set<() => void>();

export function getReadingRange(): ReadingRangePreset {
  return presetByKey(currentKey);
}

export function setReadingRange(key: string): void {
  if (key === currentKey) return;
  currentKey = key;
  try {
    window.localStorage.setItem(STORAGE_KEY, key);
  } catch {
    /* best-effort persistence */
  }
  listeners.forEach((l) => l());
}

/** Test helper — reset the store to its default without touching storage listeners' history. */
export function __resetReadingRange(): void {
  currentKey = DEFAULT_RANGE_KEY;
  listeners.forEach((l) => l());
}

export function useReadingRange(): readonly [ReadingRangePreset, (key: string) => void] {
  const subscribe = useCallback((cb: () => void) => {
    listeners.add(cb);
    return () => listeners.delete(cb);
  }, []);
  const preset = useSyncExternalStore(
    subscribe,
    () => presetByKey(currentKey),
    () => presetByKey(DEFAULT_RANGE_KEY),
  );
  const set = useCallback((key: string) => setReadingRange(key), []);
  return [preset, set] as const;
}

/**
 * The three windowed QuerySpecs derived from a preset. Grouped tiles (genre /
 * series / prolific genres) are intentionally NOT here — they stay all-time so
 * the composition/depth panels read your whole library regardless of window.
 */
export function readingSpecsForRange(p: ReadingRangePreset) {
  return {
    listeningInRange: {
      domain: "reading",
      measure: "seconds",
      over: { granularity: "none", range: { lastN: p.days } },
    },
    listeningTrend: {
      domain: "reading",
      measure: "seconds",
      over: { granularity: p.trend.granularity, range: { lastN: p.trend.lastN } },
    },
    codingTrend: {
      domain: "coding",
      measure: "seconds",
      over: { granularity: p.trend.granularity, range: { lastN: p.trend.lastN } },
    },
    finishedPerMonth: {
      domain: "reading",
      measure: "books",
      where: { kind: "leaf", dim: "status", op: "eq", values: ["read"] },
      over: { granularity: p.finished.granularity, range: { lastN: p.finished.lastN } },
    },
  } satisfies Record<string, QuerySpec>;
}
