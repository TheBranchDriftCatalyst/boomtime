import { describe, it, expect } from "vitest";
import { parseDuration, formatDuration } from "./duration";

describe("parseDuration", () => {
  it("parses bare integer as seconds (backward-compat)", () => {
    expect(parseDuration("3600")).toBe(3600);
    expect(parseDuration("0")).toBe(0);
  });

  it("parses single-unit shortforms", () => {
    expect(parseDuration("1s")).toBe(1);
    expect(parseDuration("45m")).toBe(2700);
    expect(parseDuration("1h")).toBe(3600);
    expect(parseDuration("1d")).toBe(86400);
    expect(parseDuration("7d")).toBe(604800);
    expect(parseDuration("1w")).toBe(604800);
  });

  it("composes multi-unit shortforms", () => {
    expect(parseDuration("1h30m")).toBe(5400);
    expect(parseDuration("2h30m45s")).toBe(9045);
    expect(parseDuration("1d12h")).toBe(129600);
  });

  it("accepts whitespace between components", () => {
    expect(parseDuration("1h 30m")).toBe(5400);
    expect(parseDuration(" 2d ")).toBe(172800);
  });

  it("is case-insensitive on units", () => {
    expect(parseDuration("1H")).toBe(3600);
    expect(parseDuration("1D")).toBe(86400);
  });

  it("returns null on unknown units or garbage", () => {
    expect(parseDuration("1y")).toBeNull();
    expect(parseDuration("banana")).toBeNull();
    expect(parseDuration("h1")).toBeNull(); // unit before number
    expect(parseDuration("")).toBeNull();
    expect(parseDuration("   ")).toBeNull();
  });

  it("returns null on partially-parseable input", () => {
    // "1h and 30m" → "and" survives strip
    expect(parseDuration("1h and 30m")).toBeNull();
  });
});

describe("formatDuration", () => {
  it("picks the largest unit that divides evenly", () => {
    expect(formatDuration(3600)).toBe("1h");
    expect(formatDuration(86400)).toBe("1d");
    expect(formatDuration(604800)).toBe("1w");
  });

  it("chains smaller units for remainders", () => {
    expect(formatDuration(5400)).toBe("1h30m");
    expect(formatDuration(129600)).toBe("1d12h");
    expect(formatDuration(9045)).toBe("2h30m45s");
  });

  it("handles 0 and 1s", () => {
    expect(formatDuration(0)).toBe("0s");
    expect(formatDuration(1)).toBe("1s");
  });

  it("round-trips through parseDuration for common values", () => {
    for (const v of [1, 60, 3600, 5400, 86400, 129600, 604800, 9045]) {
      expect(parseDuration(formatDuration(v))).toBe(v);
    }
  });
});
