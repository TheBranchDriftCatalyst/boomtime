// readingMonitorCadence.ts — the PURE derivations behind the Admin › Books ›
// Reading-monitor tab (gaka-books). The live socket streams raw `sample` frames
// (one per first-seen or ADVANCED Kindle last-page-read position); these
// functions turn that stream into (a) per-row deltas — Δlocation + Δt vs that
// book's previous sample — and (b) the CADENCE stats that answer the question
// the monitor exists to answer: how often does whispersync push a new furthest-
// page-read, and how far does it jump each time?
//
// Kept free of React/DOM so vitest exercises the math directly.

/** One raw sample as streamed by the reading-monitor WS `sample` frame. */
export interface RawSample {
  asin: string;
  title: string;
  location: number;
  /** Amazon's own event time for this furthest-page-read (RFC3339), if present. */
  creationTime?: string;
  /** Server observation time (RFC3339) — always present. */
  sampledAt: string;
}

/** A display row: a sample plus its deltas vs the same book's previous sample. */
export interface MonitorRow extends RawSample {
  /** Stable, monotonic id for React keys (arrival order). */
  seq: number;
  /** location − previous same-book location. undefined for a book's first sample. */
  deltaLocation?: number;
  /** eventTime − previous same-book eventTime, in seconds. undefined for the first. */
  deltaSeconds?: number;
}

/** Cadence summary across every advance (a sample with a same-book predecessor). */
export interface Cadence {
  /** Advances observed (samples that had a prior sample for the same book). */
  advances: number;
  /** Distinct books that produced at least one sample. */
  books: number;
  /** Smallest interval between consecutive advances, seconds. */
  minIntervalSec?: number;
  /** Median interval between consecutive advances, seconds. */
  medianIntervalSec?: number;
  /** Mean Δlocation per advance. */
  avgDeltaLocation?: number;
  /** Implied wall-clock seconds per 1 location unit: Σ Δt / Σ Δlocation. */
  secondsPerLocation?: number;
}

// eventTime is a sample's best timestamp: Amazon's creationTime (when the
// position was actually set — the true whispersync cadence) when present and
// parseable, else the server sampledAt (quantized to the poll interval).
function eventTimeMs(s: RawSample): number {
  const raw = s.creationTime ?? s.sampledAt;
  const t = Date.parse(raw);
  return Number.isNaN(t) ? Date.parse(s.sampledAt) : t;
}

/**
 * buildRows maps chronological samples → rows carrying per-book deltas. Input is
 * assumed in arrival (time) order; multiple books interleave freely — deltas are
 * computed against the previous sample OF THE SAME BOOK (by asin). A book's first
 * sample is a baseline: deltaLocation/deltaSeconds stay undefined.
 */
export function buildRows(samples: RawSample[]): MonitorRow[] {
  const prev = new Map<string, RawSample>();
  return samples.map((s, i) => {
    const p = prev.get(s.asin);
    prev.set(s.asin, s);
    if (!p) return { ...s, seq: i };
    const deltaSeconds = Math.round((eventTimeMs(s) - eventTimeMs(p)) / 1000);
    return {
      ...s,
      seq: i,
      deltaLocation: s.location - p.location,
      deltaSeconds,
    };
  });
}

function median(values: number[]): number | undefined {
  if (values.length === 0) return undefined;
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  return sorted.length % 2 === 0
    ? (sorted[mid - 1] + sorted[mid]) / 2
    : sorted[mid];
}

/**
 * computeCadence reduces rows → the cadence panel stats. Only advances (rows with
 * a defined deltaSeconds, i.e. a same-book predecessor) contribute. Non-positive
 * intervals (clock skew / duplicate timestamps) are dropped from the interval
 * stats so a zero can't collapse the min/median.
 */
export function computeCadence(rows: MonitorRow[]): Cadence {
  const books = new Set(rows.map((r) => r.asin)).size;
  const advances = rows.filter((r) => r.deltaSeconds !== undefined);
  const intervals = advances
    .map((r) => r.deltaSeconds as number)
    .filter((s) => s > 0);
  const deltaLocs = advances
    .map((r) => r.deltaLocation ?? 0)
    .filter((d) => d !== 0);

  const sumDt = intervals.reduce((a, b) => a + b, 0);
  const sumDloc = advances.reduce((a, r) => a + (r.deltaLocation ?? 0), 0);

  return {
    advances: advances.length,
    books,
    minIntervalSec: intervals.length ? Math.min(...intervals) : undefined,
    medianIntervalSec: median(intervals),
    avgDeltaLocation: deltaLocs.length
      ? deltaLocs.reduce((a, b) => a + b, 0) / deltaLocs.length
      : undefined,
    secondsPerLocation: sumDloc > 0 ? sumDt / sumDloc : undefined,
  };
}

/** Compact, human seconds label: "6s", "1m 12s", "—" for undefined. */
export function fmtInterval(sec?: number): string {
  if (sec === undefined) return "—";
  if (sec < 60) return `${sec}s`;
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return s === 0 ? `${m}m` : `${m}m ${s}s`;
}

/** Signed Δlocation label: "+128", "−3", "—" for undefined. */
export function fmtDeltaLocation(d?: number): string {
  if (d === undefined) return "—";
  if (d > 0) return `+${d}`;
  if (d < 0) return `−${Math.abs(d)}`;
  return "0";
}
