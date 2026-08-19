// Client-side status derivation for the Heartbeats "Source health" panel. The
// backend returns only raw lastSeen timestamps; the active/idle/stale/silent
// bucket (and a short relative label like "2h ago") is computed here so the
// thresholds live in one place and stay easy to unit-test.

export type SourceStatus = "active" | "idle" | "stale" | "silent";

const HOUR = 3600_000;
const DAY = 24 * HOUR;

// Age thresholds (inclusive upper bound), stalest-last:
//   active ≤ 24h, idle ≤ 7d, stale ≤ 30d, silent > 30d.
const STATUS_THRESHOLDS = {
  active: 1 * DAY,
  idle: 7 * DAY,
  stale: 30 * DAY,
} as const;

// Rank used for sorting: silent/stale first (highest rank), active last.
export const STATUS_RANK: Record<SourceStatus, number> = {
  silent: 0,
  stale: 1,
  idle: 2,
  active: 3,
};

/** Derive the status bucket from lastSeen relative to `now` (default: now). */
export function deriveSourceStatus(
  lastSeen: string | Date,
  now: Date = new Date(),
): SourceStatus {
  const ageMs = now.getTime() - new Date(lastSeen).getTime();
  if (ageMs <= STATUS_THRESHOLDS.active) return "active";
  if (ageMs <= STATUS_THRESHOLDS.idle) return "idle";
  if (ageMs <= STATUS_THRESHOLDS.stale) return "stale";
  return "silent";
}

/** Compact relative label, e.g. "just now", "5m ago", "2h ago", "3d ago". */
export function relativeTime(
  lastSeen: string | Date,
  now: Date = new Date(),
): string {
  const ageMs = now.getTime() - new Date(lastSeen).getTime();
  if (ageMs < 0) return "just now";
  const mins = Math.floor(ageMs / 60_000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(ageMs / HOUR);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(ageMs / DAY);
  if (days < 30) return `${days}d ago`;
  const months = Math.floor(days / 30);
  if (months < 12) return `${months}mo ago`;
  return `${Math.floor(days / 365)}y ago`;
}
