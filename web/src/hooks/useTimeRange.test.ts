import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useTimeRange } from "@/hooks/useTimeRange";

describe("useTimeRange", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-10T12:00:00Z"));
  });
  afterEach(() => vi.useRealTimers());

  it("defaults to a 15-day window and timeLimit 15", () => {
    const { result } = renderHook(() => useTimeRange());
    expect(result.current.timeLimit).toBe(15);
    expect(result.current.numDays).toBe(15);
  });

  it("setDaysFromToday updates numDays = ceil(|Δ|/86.4e6)", () => {
    const { result } = renderHook(() => useTimeRange());
    act(() => result.current.setDaysFromToday(30));
    expect(result.current.numDays).toBe(30);
    act(() => result.current.setDaysFromToday(7));
    expect(result.current.numDays).toBe(7);
  });

  it("setRange sets an explicit range + ISO strings", () => {
    const { result } = renderHook(() => useTimeRange());
    const start = new Date("2026-01-01T00:00:00Z");
    const end = new Date("2026-01-11T00:00:00Z");
    act(() => result.current.setRange(start, end));
    expect(result.current.startISO).toBe(start.toISOString());
    expect(result.current.endISO).toBe(end.toISOString());
    expect(result.current.numDays).toBe(10);
  });

  it("supports an all-time span without error", () => {
    const { result } = renderHook(() => useTimeRange());
    act(() =>
      result.current.setRange(
        new Date("2000-01-01T00:00:00Z"),
        new Date("2026-07-10T00:00:00Z"),
      ),
    );
    expect(result.current.numDays).toBeGreaterThan(3650);
  });

  it("setTimeLimit updates the gap cutoff", () => {
    const { result } = renderHook(() => useTimeRange());
    act(() => result.current.setTimeLimit(30));
    expect(result.current.timeLimit).toBe(30);
  });
});
