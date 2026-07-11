import type { ReactNode } from "react";
import { createElement } from "react";
import { act, renderHook } from "@testing-library/react";
import { MemoryRouter, useSearchParams } from "react-router";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useTimeRange } from "@/hooks/useTimeRange";

// Wrap the hook in a router at the given URL so useSearchParams resolves.
function wrapper(initialEntries: string[] = ["/"]) {
  return ({ children }: { children: ReactNode }) =>
    createElement(MemoryRouter, { initialEntries }, children);
}

// Probe both hooks together (same router context) so tests can read the URL the
// hook writes.
function useProbe() {
  const tr = useTimeRange();
  const [params] = useSearchParams();
  return { tr, params };
}

describe("useTimeRange", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-10T12:00:00Z"));
  });
  afterEach(() => {
    vi.useRealTimers();
    localStorage.clear();
  });

  it("defaults to a 15-day window and timeLimit 15", () => {
    const { result } = renderHook(() => useTimeRange(), { wrapper: wrapper() });
    expect(result.current.timeLimit).toBe(15);
    expect(result.current.numDays).toBe(15);
  });

  it("setDaysFromToday updates numDays = ceil(|Δ|/86.4e6)", () => {
    const { result } = renderHook(() => useTimeRange(), { wrapper: wrapper() });
    act(() => result.current.setDaysFromToday(30));
    expect(result.current.numDays).toBe(30);
    act(() => result.current.setDaysFromToday(7));
    expect(result.current.numDays).toBe(7);
  });

  it("setRange sets an explicit range + ISO strings", () => {
    const { result } = renderHook(() => useTimeRange(), { wrapper: wrapper() });
    const start = new Date("2026-01-01T00:00:00Z");
    const end = new Date("2026-01-11T00:00:00Z");
    act(() => result.current.setRange(start, end));
    expect(result.current.startISO).toBe(start.toISOString());
    expect(result.current.endISO).toBe(end.toISOString());
    expect(result.current.numDays).toBe(10);
  });

  it("supports an all-time span without error", () => {
    const { result } = renderHook(() => useTimeRange(), { wrapper: wrapper() });
    act(() =>
      result.current.setRange(
        new Date("2000-01-01T00:00:00Z"),
        new Date("2026-07-10T00:00:00Z"),
      ),
    );
    expect(result.current.numDays).toBeGreaterThan(3650);
  });

  it("setTimeLimit updates the gap cutoff", () => {
    const { result } = renderHook(() => useTimeRange(), { wrapper: wrapper() });
    act(() => result.current.setTimeLimit(30));
    expect(result.current.timeLimit).toBe(30);
  });

  it("reflects the selection into the URL and localStorage on change", () => {
    const { result } = renderHook(() => useProbe(), { wrapper: wrapper() });
    const start = new Date("2026-02-01T00:00:00Z");
    const end = new Date("2026-02-15T00:00:00Z");
    act(() => result.current.tr.setRange(start, end));
    act(() => result.current.tr.setTimeLimit(45));

    // URL params carry epoch millis + the limit.
    expect(result.current.params.get("start")).toBe(String(start.getTime()));
    expect(result.current.params.get("end")).toBe(String(end.getTime()));
    expect(result.current.params.get("limit")).toBe("45");

    // localStorage mirrors the same state for cross-navigation persistence.
    const stored = JSON.parse(localStorage.getItem("boomtime-timerange")!);
    expect(stored).toEqual({
      start: start.getTime(),
      end: end.getTime(),
      timeLimit: 45,
    });
  });

  it("initializes from URL params when present (shared/bookmarked link)", () => {
    const start = new Date("2026-03-01T00:00:00Z").getTime();
    const end = new Date("2026-03-10T00:00:00Z").getTime();
    const { result } = renderHook(() => useTimeRange(), {
      wrapper: wrapper([`/?start=${start}&end=${end}&limit=20`]),
    });
    expect(result.current.startISO).toBe(new Date(start).toISOString());
    expect(result.current.endISO).toBe(new Date(end).toISOString());
    expect(result.current.timeLimit).toBe(20);
    expect(result.current.numDays).toBe(9);
  });

  it("falls back to localStorage when the URL has no range params", () => {
    const start = new Date("2026-04-01T00:00:00Z").getTime();
    const end = new Date("2026-04-08T00:00:00Z").getTime();
    localStorage.setItem(
      "boomtime-timerange",
      JSON.stringify({ start, end, timeLimit: 60 }),
    );
    const { result } = renderHook(() => useTimeRange(), { wrapper: wrapper() });
    expect(result.current.startISO).toBe(new Date(start).toISOString());
    expect(result.current.timeLimit).toBe(60);
    expect(result.current.numDays).toBe(7);
  });

  it("prefers URL params over localStorage when both exist", () => {
    localStorage.setItem(
      "boomtime-timerange",
      JSON.stringify({
        start: new Date("2026-04-01T00:00:00Z").getTime(),
        end: new Date("2026-04-08T00:00:00Z").getTime(),
        timeLimit: 60,
      }),
    );
    const urlStart = new Date("2026-05-01T00:00:00Z").getTime();
    const urlEnd = new Date("2026-05-06T00:00:00Z").getTime();
    const { result } = renderHook(() => useTimeRange(), {
      wrapper: wrapper([`/?start=${urlStart}&end=${urlEnd}&limit=5`]),
    });
    expect(result.current.startISO).toBe(new Date(urlStart).toISOString());
    expect(result.current.timeLimit).toBe(5);
    expect(result.current.numDays).toBe(5);
  });
});
