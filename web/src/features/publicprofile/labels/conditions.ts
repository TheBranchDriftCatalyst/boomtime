// conditions.ts — the pure evaluator for the label DSL (gaka-364).
//
// `evaluateCondition(cond, payload)` returns true/false — no
// side-effects, no clock reads, no random. Every payload-only condition
// primitive in `types.ts` maps to one case in the switch below.
//
// If a new label needs a primitive the existing set doesn't cover, add
// it here + `types.ts` + a matching unit test in `conditions.test.ts`.
// The whole file should stay small on purpose — the DSL's value is
// keeping the extension surface tiny.
import type { Condition, Axis, CmpOp, LabelPayload } from "./types";
import { currentStreak, longestStreakInRange } from "@/features/publicprofile/grade";

/** Threshold comparison — inclusive at the threshold. See types.ts note. */
function cmp(actual: number, op: CmpOp, threshold: number): boolean {
  return op === ">=" ? actual >= threshold : actual <= threshold;
}

/** Case-insensitive axis-value lookup. wakatime axis values are canonicalized
 *  by the backend but users author catalog entries by hand — matching on
 *  lowercased names removes a whole category of "why isn't my label firing"
 *  gotchas ("Python" vs "python"). */
function findAxisEntry(
  payload: LabelPayload,
  axis: Axis,
  value: string,
): { totalSeconds: number; totalPct: number } | null {
  const list = payload[axis];
  if (!Array.isArray(list)) return null;
  const target = value.toLowerCase();
  const hit = list.find((e) => e.name.toLowerCase() === target);
  return hit ? { totalSeconds: hit.totalSeconds, totalPct: hit.totalPct } : null;
}

/** Sum of hours across an axis (for percentage denominators). */
function axisTotalSeconds(payload: LabelPayload, axis: Axis): number {
  const list = payload[axis];
  if (!Array.isArray(list)) return 0;
  return list.reduce((s, e) => s + (e.totalSeconds ?? 0), 0);
}

/** Sum of punchcard seconds — needed for percentage-of-day-or-hour conditions. */
function punchcardTotalSeconds(payload: LabelPayload): number {
  const t = payload.punchcard?.totalSeconds ?? 0;
  if (t > 0) return t;
  // Fallback: sum cells if the top-level totalSeconds is missing/zero.
  return (payload.punchcard?.cells ?? []).reduce((s, c) => s + (c.seconds ?? 0), 0);
}

/** Split dailyTotal into last-7 vs prior-7 for trend detection. Returns
 *  null when there aren't 14 days of history to compare. */
function last7VsPrior7Ratio(payload: LabelPayload): number | null {
  const daily = payload.dailyTotal ?? [];
  if (daily.length < 14) return null;
  const last7 = daily.slice(-7);
  const prior7 = daily.slice(-14, -7);
  const avg = (a: number[]) => a.reduce((s, v) => s + v, 0) / a.length;
  const lastAvg = avg(last7);
  const priorAvg = avg(prior7);
  if (priorAvg === 0) {
    // Prior week was totally silent — call it Infinity iff last week has any
    // activity, otherwise NaN-ish 0. Downstream cmp() with a finite threshold
    // handles the Infinity case naturally.
    return lastAvg > 0 ? Number.POSITIVE_INFINITY : 0;
  }
  return lastAvg / priorAvg;
}

export function evaluateCondition(cond: Condition, payload: LabelPayload): boolean {
  switch (cond.kind) {
    case "axis-time": {
      const hit = findAxisEntry(payload, cond.axis, cond.value);
      const hours = (hit?.totalSeconds ?? 0) / 3600;
      return cmp(hours, cond.op, cond.hours);
    }
    case "axis-pct": {
      const hit = findAxisEntry(payload, cond.axis, cond.value);
      // Payload's totalPct is percent (0..100); DSL expresses pct as 0..1.
      const pct = (hit?.totalPct ?? 0) / 100;
      return cmp(pct, cond.op, cond.pct);
    }
    case "top-share": {
      const list = payload[cond.axis];
      if (!Array.isArray(list) || list.length === 0) return cmp(0, cond.op, cond.pct);
      const total = axisTotalSeconds(payload, cond.axis);
      if (total === 0) return cmp(0, cond.op, cond.pct);
      const top = list[0]?.totalSeconds ?? 0;
      return cmp(top / total, cond.op, cond.pct);
    }
    case "distinct-count": {
      const list = payload[cond.axis];
      if (!Array.isArray(list)) return cmp(0, cond.op, cond.n);
      const minSec = cond.minHoursEach * 3600;
      const qualifying = list.filter((e) => (e.totalSeconds ?? 0) >= minSec).length;
      return cmp(qualifying, cond.op, cond.n);
    }
    case "punchcard-hour-pct": {
      const cells = payload.punchcard?.cells ?? [];
      const total = punchcardTotalSeconds(payload);
      if (total === 0) return cmp(0, cond.op, cond.pct);
      const hourSet = new Set(cond.hoursIn);
      const bucket = cells.reduce(
        (s, c) => (hourSet.has(c.hour) ? s + c.seconds : s),
        0,
      );
      return cmp(bucket / total, cond.op, cond.pct);
    }
    case "punchcard-dow-pct": {
      const cells = payload.punchcard?.cells ?? [];
      const total = punchcardTotalSeconds(payload);
      if (total === 0) return cmp(0, cond.op, cond.pct);
      const dowSet = new Set(cond.dowIn);
      const bucket = cells.reduce(
        (s, c) => (dowSet.has(c.dow) ? s + c.seconds : s),
        0,
      );
      return cmp(bucket / total, cond.op, cond.pct);
    }
    case "streak": {
      const days =
        cond.which === "current"
          ? currentStreak(payload.dailyTotal ?? [])
          : longestStreakInRange(payload.dailyTotal ?? []);
      return cmp(days, cond.op, cond.days);
    }
    case "daily-avg": {
      const hours = (payload.dailyAvg ?? 0) / 3600;
      return cmp(hours, cond.op, cond.hours);
    }
    case "trend": {
      const ratio = last7VsPrior7Ratio(payload);
      if (ratio === null) return false; // insufficient history — no award, no false-positive
      return cmp(ratio, cond.op, cond.ratio);
    }
    case "all":
      return cond.of.every((c) => evaluateCondition(c, payload));
    case "any":
      return cond.of.some((c) => evaluateCondition(c, payload));
    case "not":
      return !evaluateCondition(cond.of, payload);
    default: {
      // Exhaustiveness guard — TS enforces at compile time; guarded at runtime
      // for defensive belt-and-braces (a malformed manifest shouldn't crash the
      // whole UI).
      const _exhaustive: never = cond;
      void _exhaustive;
      return false;
    }
  }
}
