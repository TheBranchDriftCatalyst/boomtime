import { afterEach, describe, expect, it, vi } from "vitest";
import { formatElapsed, secondsToHms } from "@/lib/utils";

describe("secondsToHms", () => {
  it("formats hours + minutes (14 hrs 32 min)", () => {
    // 14*3600 + 32*60 = 52320
    expect(secondsToHms(52320)).toBe("14 hrs 32 mins");
  });

  it("uses singular units", () => {
    expect(secondsToHms(3600)).toBe("1 hr");
    expect(secondsToHms(3660)).toBe("1 hr 1 min");
    expect(secondsToHms(1)).toBe("1 sec");
  });

  it("shows seconds only when under a minute", () => {
    expect(secondsToHms(45)).toBe("45 secs");
    // seconds are dropped once >= 60s (matches hakatime)
    expect(secondsToHms(3661)).toBe("1 hr 1 min");
  });

  it("0/null/undefined -> '0 mins'", () => {
    expect(secondsToHms(0)).toBe("0 mins");
    expect(secondsToHms(null)).toBe("0 mins");
    expect(secondsToHms(undefined)).toBe("0 mins");
  });
});

describe("formatElapsed", () => {
  afterEach(() => vi.useRealTimers());

  it("zero-pads H/M/S", () => {
    const from = new Date("2026-07-10T00:00:00Z");
    const to = new Date("2026-07-10T01:04:09Z"); // 1h 4m 9s
    expect(formatElapsed(from, to)).toBe("1h 04m 09s");
  });

  it("drops the hour when under an hour", () => {
    const from = new Date("2026-07-10T00:00:00Z");
    expect(formatElapsed(from, new Date("2026-07-10T00:05:03Z"))).toBe(
      "5m 03s",
    );
    expect(formatElapsed(from, new Date("2026-07-10T00:00:07Z"))).toBe("7s");
  });

  it("to=null uses now()", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-10T00:00:30Z"));
    const from = new Date("2026-07-10T00:00:00Z");
    expect(formatElapsed(from, null)).toBe("30s");
  });

  it("clamps negative durations to 0", () => {
    const from = new Date("2026-07-10T01:00:00Z");
    const to = new Date("2026-07-10T00:00:00Z");
    expect(formatElapsed(from, to)).toBe("0s");
  });

  it("from=null -> '-'", () => {
    expect(formatElapsed(null, new Date())).toBe("-");
  });
});
