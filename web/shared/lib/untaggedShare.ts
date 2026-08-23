// untaggedShare.ts — small helper for the "N% untagged / browsing"
// subtitle on per-axis charts (boom-6ci). Backend filters null-axis
// heartbeats out of the per-axis pie total, so the sum of the visible
// slices is < the grand total. This helper computes the delta and
// formats it for a ChartCard subtitle.

import type { ResourceStats } from "@shared/types/api";

/**
 * Returns a subtitle string like "28% untagged / browsing" when the
 * per-axis chart sum trails the grand total by a meaningful margin
 * (>= 1 min AND >= 5% relative). Returns null otherwise (nothing to
 * disclose, or the axis is fully accounted for).
 *
 * The "1 min" absolute floor prevents noisy chips on freshly-created
 * accounts. The "5% relative" floor prevents a couple stray browser
 * pings from displaying "0.1% untagged" — that's not actionable.
 */
export function untaggedShareSubtitle(
  chartSlices: ReadonlyArray<Pick<ResourceStats, "totalSeconds">>,
  grandTotalSeconds: number | null | undefined,
  opts?: { axis?: string },
): string | null {
  if (!grandTotalSeconds || grandTotalSeconds < 60) return null;
  const shown = chartSlices.reduce((s, r) => s + (r.totalSeconds ?? 0), 0);
  const missing = grandTotalSeconds - shown;
  if (missing < 60) return null; // < 1 min gap — nothing meaningful
  const pct = (missing / grandTotalSeconds) * 100;
  if (pct < 5) return null; // < 5% — noise
  const axis = opts?.axis ?? "";
  const suffix = axis ? `${axis}-less` : "untagged";
  return `${pct.toFixed(0)}% ${suffix} / browsing`;
}
