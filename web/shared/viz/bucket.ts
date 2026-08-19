// Shared ~weekly bucketing so daily time-series stay bounded (~60 points) on
// long ranges instead of freezing the renderer on "All time". This is the same
// logic Overview/Projects already used inline, extracted for reuse by the new
// viz-council charts. The Contribution Calendar is the deliberate exception —
// it needs raw daily cells (a year is only ~465 cells, which is fine).

export const MAX_CHART_POINTS = 62;

/** Group day indices into ~equal contiguous buckets (identity when short). */
export function bucketGroups(dayCount: number): number[][] {
  if (dayCount <= MAX_CHART_POINTS) {
    return Array.from({ length: dayCount }, (_, i) => [i]);
  }
  const size = Math.ceil(dayCount / MAX_CHART_POINTS);
  const groups: number[][] = [];
  for (let i = 0; i < dayCount; i += size) {
    groups.push(
      Array.from({ length: Math.min(size, dayCount - i) }, (_, k) => i + k),
    );
  }
  return groups;
}

/** The representative (first) date ISO string for each bucket. */
export function bucketDates(groups: number[][], dates: string[]): string[] {
  return groups.map((gr) => dates[gr[0]]);
}

/**
 * The `{ start, end }` ISO date range each bucket covers. Bucketed tooltips
 * (weekly bars, streamgraph layers, momentum cells) surface this range instead
 * of the first day so hovers read "12–18 Jan" rather than the misleading
 * "12 Jan". Falls back to identity spans when the group has a single day.
 */
export function bucketRanges(
  groups: number[][],
  dates: string[],
): { start: string; end: string }[] {
  return groups.map((gr) => {
    if (gr.length === 0) return { start: "", end: "" };
    return { start: dates[gr[0]] ?? "", end: dates[gr[gr.length - 1]] ?? "" };
  });
}

/** Sum a daily numeric series into bucket totals. */
export function bucketSum(groups: number[][], arr: number[]): number[] {
  return groups.map((gr) => gr.reduce((s, i) => s + (arr[i] ?? 0), 0));
}

/** Average a daily numeric series over each bucket (for ratios/rates). */
export function bucketAvg(groups: number[][], arr: number[]): number[] {
  return groups.map((gr) => {
    if (gr.length === 0) return 0;
    const sum = gr.reduce((s, i) => s + (arr[i] ?? 0), 0);
    return sum / gr.length;
  });
}
