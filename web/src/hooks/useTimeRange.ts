import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router";
import { DEFAULT_NUM_DAYS, DEFAULT_TIME_LIMIT } from "@/lib/config";
import { loadStored, saveStored } from "@/lib/persist";

export interface TimeRange {
  start: Date;
  end: Date;
  numDays: number;
  timeLimit: number;
}

export interface TimeRangeControls extends TimeRange {
  startISO: string;
  endISO: string;
  setDaysFromToday: (n: number) => void;
  setRange: (start: Date, end: Date) => void;
  setTimeLimit: (n: number) => void;
}

// Persisted shape: epoch millis for the range bounds + the gap cutoff. Millis
// round-trip exactly through the URL and localStorage (no timezone/format
// ambiguity), so a shared link or a reload restores the identical window.
interface RangeState {
  start: number;
  end: number;
  timeLimit: number;
}

const STORAGE_KEY = "boomtime-timerange";

function daysAgo(n: number): Date {
  const d = new Date();
  d.setDate(d.getDate() - n);
  return d;
}

function dateDiff(a: Date, b: Date): number {
  return Math.ceil(Math.abs(a.getTime() - b.getTime()) / 86_400_000);
}

function defaults(): RangeState {
  return {
    start: daysAgo(DEFAULT_NUM_DAYS).getTime(),
    end: Date.now(),
    timeLimit: DEFAULT_TIME_LIMIT,
  };
}

// Parse ?start&end&limit (epoch millis) into a RangeState, or null if absent
// or malformed — callers fall back to storage/defaults.
function fromParams(sp: URLSearchParams): RangeState | null {
  const rawStart = sp.get("start");
  const rawEnd = sp.get("end");
  if (rawStart == null || rawEnd == null) return null;
  const start = Number(rawStart);
  const end = Number(rawEnd);
  if (!Number.isFinite(start) || !Number.isFinite(end)) return null;
  const limit = Number(sp.get("limit"));
  return {
    start,
    end,
    timeLimit: Number.isFinite(limit) && limit > 0 ? limit : DEFAULT_TIME_LIMIT,
  };
}

function sameRange(a: RangeState, b: RangeState): boolean {
  return a.start === b.start && a.end === b.end && a.timeLimit === b.timeLimit;
}

/**
 * Shared date-range + time-limit state that STICKS across navigation.
 *
 * Precedence on mount: URL params (?start&end&limit) → localStorage → defaults.
 * Every change writes the URL (replace, so range tweaks don't spam history) and
 * localStorage, so a param-less navigation (a sidebar link) rehydrates from
 * storage while a shared/bookmarked link or reload restores from the URL. The
 * URL is the source of truth; localStorage is the durable fallback carrier.
 */
export function useTimeRange(): TimeRangeControls {
  const [searchParams, setSearchParams] = useSearchParams();

  const [state, setState] = useState<RangeState>(
    () => fromParams(searchParams) ?? loadStored<RangeState | null>(STORAGE_KEY, null) ?? defaults(),
  );

  // Latest state for setters/effects that must not re-run on every change.
  const stateRef = useRef(state);
  stateRef.current = state;

  const writeUrl = useCallback(
    (next: RangeState) => {
      setSearchParams(
        (prev) => {
          const p = new URLSearchParams(prev);
          p.set("start", String(next.start));
          p.set("end", String(next.end));
          p.set("limit", String(next.timeLimit));
          return p;
        },
        { replace: true },
      );
    },
    [setSearchParams],
  );

  const commit = useCallback(
    (next: RangeState) => {
      setState(next);
      saveStored(STORAGE_KEY, next);
      writeUrl(next);
    },
    [writeUrl],
  );

  // Keep state in sync when the URL changes underneath us — the back/forward
  // buttons and shared links. Guarded against the writes we just made (and
  // sticks an incoming shared range into storage so it survives later nav).
  useEffect(() => {
    const p = fromParams(searchParams);
    if (!p) return;
    if (!sameRange(p, stateRef.current)) setState(p);
    saveStored(STORAGE_KEY, p);
  }, [searchParams]);

  const setDaysFromToday = useCallback(
    (n: number) =>
      commit({ ...stateRef.current, start: daysAgo(n).getTime(), end: Date.now() }),
    [commit],
  );

  const setRange = useCallback(
    (s: Date, e: Date) =>
      commit({ ...stateRef.current, start: s.getTime(), end: e.getTime() }),
    [commit],
  );

  const setTimeLimit = useCallback(
    (n: number) => commit({ ...stateRef.current, timeLimit: n }),
    [commit],
  );

  const start = useMemo(() => new Date(state.start), [state.start]);
  const end = useMemo(() => new Date(state.end), [state.end]);
  const numDays = useMemo(() => dateDiff(start, end), [start, end]);

  return {
    start,
    end,
    numDays,
    timeLimit: state.timeLimit,
    startISO: start.toISOString(),
    endISO: end.toISOString(),
    setDaysFromToday,
    setRange,
    setTimeLimit,
  };
}
