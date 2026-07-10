import { useCallback, useMemo, useState } from "react";
import { DEFAULT_NUM_DAYS, DEFAULT_TIME_LIMIT } from "@/lib/config";

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

function daysAgo(n: number): Date {
  const d = new Date();
  d.setDate(d.getDate() - n);
  return d;
}

function dateDiff(a: Date, b: Date): number {
  return Math.ceil(Math.abs(a.getTime() - b.getTime()) / 86_400_000);
}

/** Shared date-range + time-limit state, ported from TimeRange.js. */
export function useTimeRange(): TimeRangeControls {
  const [start, setStart] = useState<Date>(() => daysAgo(DEFAULT_NUM_DAYS));
  const [end, setEnd] = useState<Date>(() => new Date());
  const [timeLimit, setTimeLimitState] = useState<number>(DEFAULT_TIME_LIMIT);

  const setDaysFromToday = useCallback((n: number) => {
    setStart(daysAgo(n));
    setEnd(new Date());
  }, []);

  const setRange = useCallback((s: Date, e: Date) => {
    setStart(s);
    setEnd(e);
  }, []);

  const setTimeLimit = useCallback((n: number) => setTimeLimitState(n), []);

  const numDays = useMemo(() => dateDiff(start, end), [start, end]);

  return {
    start,
    end,
    numDays,
    timeLimit,
    startISO: start.toISOString(),
    endISO: end.toISOString(),
    setDaysFromToday,
    setRange,
    setTimeLimit,
  };
}
