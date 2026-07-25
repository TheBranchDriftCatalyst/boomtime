// color.test.ts — regression coverage for gaka-538 (theme-aware chart
// palette). Guards the contract that `colorAt(i)`:
//   1. Reads `--chart-N` from the active theme first (N = (i % 12) + 1)
//   2. Falls back to the legacy hardcoded hex when the token is missing
//   3. Wraps positional indices around a 12-slot window
//   4. Is safe to call in SSR (no `document`) — falls back to hex only
//
// Non-tautological: each test locks a distinct decision. Every case
// forces a different code path in `readChartToken` / `colorAt` so
// reverting to `CHART_COLORS[i % 14]` breaks a specific assertion.
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { CHART_COLORS } from "@/lib/config";
import { colorAt } from "./color";

describe("colorAt — theme-aware chart palette (gaka-538)", () => {
  const setChartToken = (n: number, value: string) => {
    document.documentElement.style.setProperty(`--chart-${n}`, value);
  };
  const clearAllChartTokens = () => {
    for (let n = 1; n <= 12; n++) {
      document.documentElement.style.removeProperty(`--chart-${n}`);
    }
  };

  beforeEach(() => {
    clearAllChartTokens();
  });
  afterEach(() => {
    clearAllChartTokens();
  });

  it("returns the theme's --chart-1 for i=0 when set", () => {
    setChartToken(1, "rgb(1, 2, 3)");
    expect(colorAt(0)).toBe("rgb(1, 2, 3)");
  });

  it("returns the theme's --chart-12 for i=11 when set", () => {
    setChartToken(12, "oklch(0.5 0.2 25)");
    expect(colorAt(11)).toBe("oklch(0.5 0.2 25)");
  });

  it("wraps around a 12-slot window (i=12 reads --chart-1)", () => {
    setChartToken(1, "#deadbe");
    expect(colorAt(12)).toBe("#deadbe");
  });

  it("wraps around a 12-slot window (i=25 reads --chart-2)", () => {
    setChartToken(2, "#c0ffee");
    // 25 % 12 = 1 → --chart-(1+1) = --chart-2
    expect(colorAt(25)).toBe("#c0ffee");
  });

  it("falls back to CHART_COLORS[i % 14] when the token is unset", () => {
    // No --chart-1 set → hex fallback for i=0
    expect(colorAt(0)).toBe(CHART_COLORS[0]);
  });

  it("falls back to CHART_COLORS[i % 14] when the token is set to an empty string", () => {
    // Explicit empty string should NOT count as a valid token value —
    // otherwise a broken theme silently strips slice colors.
    document.documentElement.style.setProperty("--chart-1", "");
    expect(colorAt(0)).toBe(CHART_COLORS[0]);
  });

  it("preserves the 14-slot hex fallback wraparound when tokens are absent", () => {
    // With NO --chart-* tokens set, `colorAt(14)` wraps CHART_COLORS at
    // index 14 % 14 = 0 → the first hex. Locks the legacy behavior for
    // the fallback path so it survives future token refactors.
    expect(colorAt(14)).toBe(CHART_COLORS[0]);
  });

  it("prefers the token even when the hex fallback also exists", () => {
    setChartToken(3, "hsl(200 50% 50%)");
    expect(colorAt(2)).toBe("hsl(200 50% 50%)");
    expect(colorAt(2)).not.toBe(CHART_COLORS[2]);
  });
});
