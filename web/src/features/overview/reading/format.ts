// format.ts — compact human formatters for the Reading tiles. Kept local to the
// reading feature (the Overview's `secondsToHms` reads "3 hrs 12 mins" which is
// too wide for a KPI tile / a bar label), tiny and pure so the tests can assert
// exact strings.

/** Seconds → a compact "3h 12m" / "12m" / "0m" reading. Rounds to the minute. */
export function fmtHoursMin(seconds: number | null | undefined): string {
  const s = Math.max(0, Math.round(Number(seconds ?? 0)));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (h === 0 && m === 0) return "0m";
  if (h === 0) return `${m}m`;
  if (m === 0) return `${h}h`;
  return `${h}h ${m}m`;
}

/** Runtime minutes → "12h 30m" (the `runtime` measure sums minutes). */
export function fmtRuntimeMin(minutes: number | null | undefined): string {
  return fmtHoursMin(Math.round(Number(minutes ?? 0)) * 60);
}

/** An RFC3339 bucket (month/week granularity) → a short "Jan 2026" label.
 * Falls back to the raw string if it isn't a parseable date. */
export function fmtMonthLabel(bucket: string): string {
  const d = new Date(bucket);
  if (Number.isNaN(d.getTime())) return bucket;
  return d.toLocaleDateString(undefined, {
    month: "short",
    year: "numeric",
    timeZone: "UTC",
  });
}

/** An RFC3339 bucket → a short "12 Jan" week label. */
export function fmtWeekLabel(bucket: string): string {
  const d = new Date(bucket);
  if (Number.isNaN(d.getTime())) return bucket;
  return d.toLocaleDateString(undefined, {
    day: "numeric",
    month: "short",
    timeZone: "UTC",
  });
}
