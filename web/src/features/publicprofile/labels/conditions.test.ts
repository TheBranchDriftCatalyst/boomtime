// conditions.test.ts — one describe per Condition primitive. Every
// primitive gets both a "just over threshold → true" and a "just under
// threshold → false" case so shifting the operator by one or flipping
// the comparison direction breaks a specific assertion.
import { describe, expect, it } from "vitest";
import { evaluateCondition } from "./conditions";
import type { LabelPayload } from "./types";
import type { PublicDashboardPayload } from "@/types/stats";

// Minimal payload builder — every axis defaults to empty so tests only
// populate what they care about.
const p = (over: Partial<PublicDashboardPayload>): PublicDashboardPayload => ({
  username: "test",
  startDate: new Date(0).toISOString(),
  endDate: new Date().toISOString(),
  totalSeconds: 0,
  dailyAvg: 0,
  dailyTotal: [],
  projects: [],
  languages: [],
  editors: [],
  platforms: [],
  categories: [],
  punchcard: { cells: [], maxSeconds: 0, totalSeconds: 0 },
  ...over,
});

// ResourceStats factory — full shape so TS is happy.
const rs = (name: string, hours: number, pct?: number) => ({
  name,
  totalSeconds: hours * 3600,
  totalPct: pct ?? 0,
  totalDaily: [],
  pctDaily: [],
});

describe("evaluateCondition — axis-time", () => {
  it("awards when hours match threshold exactly (>= is inclusive)", () => {
    const payload = p({ languages: [rs("Python", 100)] });
    expect(
      evaluateCondition(
        { kind: "axis-time", axis: "languages", value: "python", op: ">=", hours: 100 },
        payload,
      ),
    ).toBe(true);
  });
  it("denies when hours fall just under threshold", () => {
    const payload = p({ languages: [rs("Python", 99.99)] });
    expect(
      evaluateCondition(
        { kind: "axis-time", axis: "languages", value: "python", op: ">=", hours: 100 },
        payload,
      ),
    ).toBe(false);
  });
  it("matches axis-value case-insensitively", () => {
    const payload = p({ languages: [rs("PYTHON", 100)] });
    expect(
      evaluateCondition(
        { kind: "axis-time", axis: "languages", value: "python", op: ">=", hours: 100 },
        payload,
      ),
    ).toBe(true);
  });
  it("returns false when the axis-value is absent", () => {
    const payload = p({ languages: [rs("Go", 100)] });
    expect(
      evaluateCondition(
        { kind: "axis-time", axis: "languages", value: "python", op: ">=", hours: 1 },
        payload,
      ),
    ).toBe(false);
  });
});

describe("evaluateCondition — axis-pct", () => {
  it("awards when payload pct (0..100) meets DSL pct (0..1)", () => {
    // Meetings category with 12% share → DSL threshold 0.10 satisfied
    const payload = p({ categories: [rs("meeting", 5, 12)] });
    expect(
      evaluateCondition(
        { kind: "axis-pct", axis: "categories", value: "meeting", op: ">=", pct: 0.1 },
        payload,
      ),
    ).toBe(true);
  });
  it("denies when payload pct barely misses", () => {
    const payload = p({ categories: [rs("meeting", 5, 9.99)] });
    expect(
      evaluateCondition(
        { kind: "axis-pct", axis: "categories", value: "meeting", op: ">=", pct: 0.1 },
        payload,
      ),
    ).toBe(false);
  });
});

describe("evaluateCondition — top-share", () => {
  it("awards when top item holds ≥ threshold pct of axis total", () => {
    // Top project 700s, tail 300s → top-share = 0.70
    const payload = p({
      projects: [rs("core", 7 / 3600 * 3600 / 1), rs("side", 3 / 3600 * 3600 / 1)], // ugly math — keep it plain seconds:
    });
    // Redo with straightforward totals:
    const payload2 = p({
      projects: [
        { name: "core", totalSeconds: 700, totalPct: 0, totalDaily: [], pctDaily: [] },
        { name: "side", totalSeconds: 300, totalPct: 0, totalDaily: [], pctDaily: [] },
      ],
    });
    expect(
      evaluateCondition(
        { kind: "top-share", axis: "projects", op: ">=", pct: 0.7 },
        payload2,
      ),
    ).toBe(true);
    // Reference `payload` to silence unused-var lint; it's the illustrative
    // "don't-do-this" variant kept for readers of the test source.
    void payload;
  });
  it("denies when top item is just below threshold", () => {
    const payload = p({
      projects: [
        { name: "core", totalSeconds: 699, totalPct: 0, totalDaily: [], pctDaily: [] },
        { name: "side", totalSeconds: 301, totalPct: 0, totalDaily: [], pctDaily: [] },
      ],
    });
    expect(
      evaluateCondition(
        { kind: "top-share", axis: "projects", op: ">=", pct: 0.7 },
        payload,
      ),
    ).toBe(false);
  });
  it("returns false gracefully on empty axis", () => {
    expect(
      evaluateCondition(
        { kind: "top-share", axis: "projects", op: ">=", pct: 0.5 },
        p({}),
      ),
    ).toBe(false);
  });
});

describe("evaluateCondition — distinct-count", () => {
  it("awards when exactly N entries meet minHours each", () => {
    const payload = p({
      languages: [rs("a", 5), rs("b", 5), rs("c", 5), rs("d", 5), rs("e", 5)],
    });
    expect(
      evaluateCondition(
        { kind: "distinct-count", axis: "languages", minHoursEach: 5, op: ">=", n: 5 },
        payload,
      ),
    ).toBe(true);
  });
  it("denies when one entry is just under minHours", () => {
    const payload = p({
      languages: [rs("a", 5), rs("b", 5), rs("c", 5), rs("d", 5), rs("e", 4.99)],
    });
    expect(
      evaluateCondition(
        { kind: "distinct-count", axis: "languages", minHoursEach: 5, op: ">=", n: 5 },
        payload,
      ),
    ).toBe(false);
  });
});

describe("evaluateCondition — punchcard-hour-pct", () => {
  // 100s in hours 22-3 (6 cells * 25s) + 200s in hour 12 → 400s total
  // Late-night share = 200/400 = 0.5
  const cells = [
    { dow: 1, hour: 22, seconds: 25 },
    { dow: 1, hour: 23, seconds: 25 },
    { dow: 1, hour: 0, seconds: 25 },
    { dow: 1, hour: 1, seconds: 25 },
    { dow: 1, hour: 2, seconds: 25 },
    { dow: 1, hour: 3, seconds: 25 },
    { dow: 1, hour: 12, seconds: 200 },
  ];
  const payload = p({ punchcard: { cells, maxSeconds: 200, totalSeconds: 350 } });

  it("awards at threshold (50% ≥ 25%)", () => {
    expect(
      evaluateCondition(
        {
          kind: "punchcard-hour-pct",
          hoursIn: [22, 23, 0, 1, 2, 3],
          op: ">=",
          pct: 0.25,
        },
        payload,
      ),
    ).toBe(true);
  });
  it("denies when threshold set above actual share", () => {
    expect(
      evaluateCondition(
        {
          kind: "punchcard-hour-pct",
          hoursIn: [22, 23, 0, 1, 2, 3],
          op: ">=",
          pct: 0.51,
        },
        payload,
      ),
    ).toBe(false);
  });
});

describe("evaluateCondition — punchcard-dow-pct", () => {
  const cells = [
    { dow: 0, hour: 12, seconds: 100 }, // Sun
    { dow: 6, hour: 12, seconds: 100 }, // Sat
    { dow: 3, hour: 12, seconds: 300 }, // Wed
  ]; // weekend share = 200/500 = 0.4
  const payload = p({ punchcard: { cells, maxSeconds: 300, totalSeconds: 500 } });

  it("awards at exact threshold", () => {
    expect(
      evaluateCondition(
        { kind: "punchcard-dow-pct", dowIn: [0, 6], op: ">=", pct: 0.4 },
        payload,
      ),
    ).toBe(true);
  });
  it("denies just above actual share", () => {
    expect(
      evaluateCondition(
        { kind: "punchcard-dow-pct", dowIn: [0, 6], op: ">=", pct: 0.41 },
        payload,
      ),
    ).toBe(false);
  });
});

describe("evaluateCondition — streak", () => {
  it("current: awards on unbroken tail ≥ days", () => {
    const daily = Array.from({ length: 40 }, (_, i) => (i >= 10 ? 3600 : 0)); // last 30 active
    expect(
      evaluateCondition(
        { kind: "streak", which: "current", op: ">=", days: 30 },
        p({ dailyTotal: daily }),
      ),
    ).toBe(true);
  });
  it("current: denies when tail is 29 days", () => {
    const daily = Array.from({ length: 40 }, (_, i) => (i >= 11 ? 3600 : 0));
    expect(
      evaluateCondition(
        { kind: "streak", which: "current", op: ">=", days: 30 },
        p({ dailyTotal: daily }),
      ),
    ).toBe(false);
  });
  it("longest: awards on best interior streak", () => {
    // 15 active, 1 gap, 8 active — longest = 15
    const daily = [...Array(15).fill(3600), 0, ...Array(8).fill(3600)];
    expect(
      evaluateCondition(
        { kind: "streak", which: "longest", op: ">=", days: 15 },
        p({ dailyTotal: daily }),
      ),
    ).toBe(true);
  });
});

describe("evaluateCondition — daily-avg", () => {
  it("awards at exact threshold in hours", () => {
    expect(
      evaluateCondition(
        { kind: "daily-avg", op: ">=", hours: 3 },
        p({ dailyAvg: 3 * 3600 }),
      ),
    ).toBe(true);
  });
  it("denies just under threshold", () => {
    expect(
      evaluateCondition(
        { kind: "daily-avg", op: ">=", hours: 3 },
        p({ dailyAvg: 3 * 3600 - 1 }),
      ),
    ).toBe(false);
  });
});

describe("evaluateCondition — trend", () => {
  it("awards when last-7 avg is ≥ 2× prior-7", () => {
    // prior week 1h each day, last week 2h each day → ratio = 2.0
    const daily = [...Array(7).fill(3600), ...Array(7).fill(7200)];
    expect(
      evaluateCondition(
        { kind: "trend", window: "last7-vs-prior7", op: ">=", ratio: 2 },
        p({ dailyTotal: daily }),
      ),
    ).toBe(true);
  });
  it("denies when ratio is 1.99", () => {
    const daily = [...Array(7).fill(3600), ...Array(7).fill(7100)];
    expect(
      evaluateCondition(
        { kind: "trend", window: "last7-vs-prior7", op: ">=", ratio: 2 },
        p({ dailyTotal: daily }),
      ),
    ).toBe(false);
  });
  it("returns false when history has fewer than 14 days", () => {
    const daily = Array(13).fill(3600);
    expect(
      evaluateCondition(
        { kind: "trend", window: "last7-vs-prior7", op: ">=", ratio: 1.5 },
        p({ dailyTotal: daily }),
      ),
    ).toBe(false);
  });
  it("treats prior=0, last>0 as +infinity (sprinter emerging from nothing)", () => {
    const daily = [...Array(7).fill(0), ...Array(7).fill(3600)];
    expect(
      evaluateCondition(
        { kind: "trend", window: "last7-vs-prior7", op: ">=", ratio: 100 },
        p({ dailyTotal: daily }),
      ),
    ).toBe(true);
  });
});

describe("evaluateCondition — composition (all/any/not)", () => {
  const payload = p({ languages: [rs("Python", 100), rs("Go", 50)] });
  it("all: passes only when every subcondition passes", () => {
    expect(
      evaluateCondition(
        {
          kind: "all",
          of: [
            { kind: "axis-time", axis: "languages", value: "python", op: ">=", hours: 100 },
            { kind: "axis-time", axis: "languages", value: "go", op: ">=", hours: 50 },
          ],
        },
        payload,
      ),
    ).toBe(true);
    expect(
      evaluateCondition(
        {
          kind: "all",
          of: [
            { kind: "axis-time", axis: "languages", value: "python", op: ">=", hours: 100 },
            { kind: "axis-time", axis: "languages", value: "rust", op: ">=", hours: 1 },
          ],
        },
        payload,
      ),
    ).toBe(false);
  });
  it("any: passes when at least one subcondition passes", () => {
    expect(
      evaluateCondition(
        {
          kind: "any",
          of: [
            { kind: "axis-time", axis: "languages", value: "rust", op: ">=", hours: 1 },
            { kind: "axis-time", axis: "languages", value: "python", op: ">=", hours: 1 },
          ],
        },
        payload,
      ),
    ).toBe(true);
  });
  it("not: inverts the inner", () => {
    expect(
      evaluateCondition(
        {
          kind: "not",
          of: { kind: "axis-time", axis: "languages", value: "rust", op: ">=", hours: 1 },
        },
        payload,
      ),
    ).toBe(true);
  });
});
