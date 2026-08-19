// formatCondition — turn a Condition JSON tree into a short human string
// like "≥ 100h in Python (languages)" or "current streak ≥ 30 days".
// Used by the LabelChip tooltip to explain what triggers each label.
//
// Keeps every render pure + deterministic; no i18n today — English only.
// If a new Condition variant lands and formatCondition misses it, the
// fallback returns the raw kind name in monospace so it's obviously
// unfinished rather than silently dropped.

import type { Condition, CmpOp } from "./types";

const OP: Record<CmpOp, string> = { ">=": "≥", "<=": "≤" };

/** Pretty-prints an axis name for display: "languages" → "Python (languages)". */
function axisValue(axis: string, value: string): string {
  return `${value} (${axis})`;
}

/** Pretty-prints a percentage: 0.15 → "15%". */
function pct(p: number): string {
  return `${Math.round(p * 100)}%`;
}

/** Human-readable hour range: [22,23,0,1,2] → "22:00–02:00" (handles wrap). */
function hourRange(hours: number[]): string {
  if (hours.length === 0) return "any hour";
  // Best-effort: assume contiguous or wrapping — display first/last.
  const min = Math.min(...hours);
  const max = Math.max(...hours);
  // Detect wrap (contains both a late hour like 22 and an early like 2).
  const hasLate = hours.some((h) => h >= 20);
  const hasEarly = hours.some((h) => h <= 4);
  if (hasLate && hasEarly) {
    const late = Math.min(...hours.filter((h) => h >= 20));
    const early = Math.max(...hours.filter((h) => h <= 4));
    return `${String(late).padStart(2, "0")}:00–${String(early + 1).padStart(2, "0")}:00`;
  }
  return `${String(min).padStart(2, "0")}:00–${String(max + 1).padStart(2, "0")}:00`;
}

const DOW_NAMES = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
function dowList(dows: number[]): string {
  return dows.map((d) => DOW_NAMES[d] ?? String(d)).join(" + ");
}

/**
 * Render a Condition tree as a single-line string. Composite kinds
 * (all/any/not) recurse with a bracketed join so nesting stays readable.
 */
export function formatCondition(cond: Condition): string {
  switch (cond.kind) {
    case "axis-time":
      return `${OP[cond.op]} ${cond.hours}h in ${axisValue(cond.axis, cond.value)}`;
    case "axis-time-sum":
      return `${OP[cond.op]} ${cond.hours}h combined across ${cond.values.join(" + ")} (${cond.axis})`;
    case "axis-pct":
      return `${OP[cond.op]} ${pct(cond.pct)} of ${axisValue(cond.axis, cond.value)}`;
    case "top-share":
      return `top ${cond.axis} entry holds ${OP[cond.op]} ${pct(cond.pct)}`;
    case "distinct-count":
      return `${OP[cond.op]} ${cond.n} distinct ${cond.axis} with ${OP[">="]} ${cond.minHoursEach}h each`;
    case "punchcard-hour-pct":
      return `${OP[cond.op]} ${pct(cond.pct)} of activity between ${hourRange(cond.hoursIn)}`;
    case "punchcard-dow-pct":
      return `${OP[cond.op]} ${pct(cond.pct)} of activity on ${dowList(cond.dowIn)}`;
    case "streak":
      return `${cond.which} streak ${OP[cond.op]} ${cond.days} days`;
    case "daily-avg":
      return `daily average ${OP[cond.op]} ${cond.hours}h`;
    case "trend":
      return `last 7-day average ${OP[cond.op]} ${cond.ratio}× prior 7-day average`;
    case "all":
      return cond.of.map(formatCondition).join(" AND ");
    case "any":
      return cond.of.map((c) => `(${formatCondition(c)})`).join(" OR ");
    case "not":
      return `NOT (${formatCondition(cond.of)})`;
  }
  // Fallback — a future variant added to Condition without a case here.
  const unknown = cond as { kind?: string };
  return `[unrecognized condition: ${unknown.kind ?? "?"}]`;
}
