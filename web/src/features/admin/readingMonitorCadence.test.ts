// readingMonitorCadence.test.ts — the reading-monitor math: per-book deltas +
// the cadence stats. Pure functions, no DOM.
import { describe, expect, it } from "vitest";
import {
  buildRows,
  computeCadence,
  fmtDeltaLocation,
  fmtInterval,
  type RawSample,
} from "./readingMonitorCadence";

// Helper: a sample whose creationTime IS its cadence clock (so Δt is exact).
function s(
  asin: string,
  title: string,
  location: number,
  creationTime: string,
): RawSample {
  return { asin, title, location, creationTime, sampledAt: creationTime };
}

describe("buildRows", () => {
  it("computes Δlocation + Δt vs the same book's previous sample; first is a baseline", () => {
    const rows = buildRows([
      s("A", "Book A", 100, "2026-08-13T10:00:00Z"),
      s("A", "Book A", 150, "2026-08-13T10:00:12Z"), // +50 over 12s
      s("A", "Book A", 190, "2026-08-13T10:00:30Z"), // +40 over 18s
    ]);
    expect(rows[0].deltaLocation).toBeUndefined();
    expect(rows[0].deltaSeconds).toBeUndefined();
    expect(rows[1].deltaLocation).toBe(50);
    expect(rows[1].deltaSeconds).toBe(12);
    expect(rows[2].deltaLocation).toBe(40);
    expect(rows[2].deltaSeconds).toBe(18);
  });

  it("keeps two interleaved books independent (delta is per-asin)", () => {
    const rows = buildRows([
      s("A", "Book A", 100, "2026-08-13T10:00:00Z"),
      s("B", "Book B", 500, "2026-08-13T10:00:03Z"),
      s("A", "Book A", 140, "2026-08-13T10:00:10Z"), // A: +40 over 10s
      s("B", "Book B", 560, "2026-08-13T10:00:15Z"), // B: +60 over 12s
    ]);
    // Book B's first sample is a baseline even though it's not row 0.
    expect(rows[1].deltaLocation).toBeUndefined();
    expect(rows[2].deltaLocation).toBe(40);
    expect(rows[2].deltaSeconds).toBe(10);
    expect(rows[3].deltaLocation).toBe(60);
    expect(rows[3].deltaSeconds).toBe(12);
  });

  it("prefers creationTime over sampledAt for Δt when present", () => {
    const rows = buildRows([
      {
        asin: "A",
        title: "A",
        location: 10,
        creationTime: "2026-08-13T10:00:00Z",
        sampledAt: "2026-08-13T10:00:05Z", // poll saw it 5s later
      },
      {
        asin: "A",
        title: "A",
        location: 20,
        creationTime: "2026-08-13T10:00:20Z",
        sampledAt: "2026-08-13T10:00:29Z",
      },
    ]);
    // Δt uses creationTime (20s), not sampledAt (24s).
    expect(rows[1].deltaSeconds).toBe(20);
  });
});

describe("computeCadence", () => {
  it("summarizes advances, min/median interval, avg Δloc, and sec/location", () => {
    const rows = buildRows([
      s("A", "Book A", 100, "2026-08-13T10:00:00Z"),
      s("A", "Book A", 150, "2026-08-13T10:00:10Z"), // +50 / 10s
      s("A", "Book A", 190, "2026-08-13T10:00:30Z"), // +40 / 20s
      s("A", "Book A", 250, "2026-08-13T10:00:45Z"), // +60 / 15s
    ]);
    const c = computeCadence(rows);
    expect(c.advances).toBe(3);
    expect(c.books).toBe(1);
    expect(c.minIntervalSec).toBe(10);
    expect(c.medianIntervalSec).toBe(15); // median of [10,20,15] = 15
    expect(c.avgDeltaLocation).toBeCloseTo((50 + 40 + 60) / 3); // 50
    // sec/location = Σ Δt / Σ Δloc = (10+20+15) / (50+40+60) = 45/150 = 0.3
    expect(c.secondsPerLocation).toBeCloseTo(0.3);
  });

  it("returns undefined stats (not NaN/crash) with no advances", () => {
    const rows = buildRows([s("A", "Book A", 100, "2026-08-13T10:00:00Z")]);
    const c = computeCadence(rows);
    expect(c.advances).toBe(0);
    expect(c.minIntervalSec).toBeUndefined();
    expect(c.medianIntervalSec).toBeUndefined();
    expect(c.avgDeltaLocation).toBeUndefined();
    expect(c.secondsPerLocation).toBeUndefined();
  });

  it("drops non-positive intervals from the interval stats", () => {
    // Two samples share a timestamp (Δt=0) → that advance's interval is ignored.
    const rows = buildRows([
      s("A", "A", 100, "2026-08-13T10:00:00Z"),
      s("A", "A", 130, "2026-08-13T10:00:00Z"), // Δt 0 → dropped from intervals
      s("A", "A", 160, "2026-08-13T10:00:12Z"), // Δt 12 → counts
    ]);
    const c = computeCadence(rows);
    expect(c.advances).toBe(2);
    expect(c.minIntervalSec).toBe(12);
    expect(c.medianIntervalSec).toBe(12);
  });
});

describe("formatters", () => {
  it("fmtInterval renders seconds/minutes and — for undefined", () => {
    expect(fmtInterval(undefined)).toBe("—");
    expect(fmtInterval(6)).toBe("6s");
    expect(fmtInterval(72)).toBe("1m 12s");
    expect(fmtInterval(120)).toBe("2m");
  });
  it("fmtDeltaLocation signs the delta", () => {
    expect(fmtDeltaLocation(undefined)).toBe("—");
    expect(fmtDeltaLocation(128)).toBe("+128");
    expect(fmtDeltaLocation(-3)).toBe("−3");
    expect(fmtDeltaLocation(0)).toBe("0");
  });
});
