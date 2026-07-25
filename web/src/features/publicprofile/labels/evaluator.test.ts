// evaluator.test.ts — walks the evaluator's promises:
//   1. Empty payload → zero awards (drives the hero "NEW OPERATOR" fallback).
//   2. Tier dedupe: when multiple tier thresholds pass for the same
//      axis-value, only the highest tier is awarded.
//   3. Rank sort: awards come back sorted rank-desc.
//   4. Non-tier awards are additive (no dedupe).
//   5. Custom catalog override lets tests seed a small manifest.
import { describe, expect, it } from "vitest";
import { evaluate } from "./evaluator";
import { tierLabels } from "./tierLabels";
import type { LabelSpec } from "./types";
import type { PublicDashboardPayload } from "@/types/stats";

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

const rs = (name: string, hours: number, pct?: number) => ({
  name,
  totalSeconds: hours * 3600,
  totalPct: pct ?? 0,
  totalDaily: [],
  pctDaily: [],
});

describe("evaluate — default catalog against empty payload", () => {
  it("awards zero labels — powers the NEW OPERATOR hero fallback", () => {
    expect(evaluate(p({}))).toHaveLength(0);
  });
});

describe("evaluate — tier dedupe", () => {
  it("keeps only the highest tier reached per tierKey", () => {
    // Python at 600h clears novice (5), apprentice (20), adept (100),
    // master (500), but not legend (2000). Expect ONE award = master.
    const catalog: LabelSpec[] = tierLabels({
      axis: "languages",
      value: "python",
      rank: 100,
      thresholds: { novice: 5, apprentice: 20, adept: 100, master: 500, legend: 2000 },
    });
    const awards = evaluate(p({ languages: [rs("python", 600)] }), { catalog });
    expect(awards).toHaveLength(1);
    expect(awards[0].tier).toBe("master");
    expect(awards[0].id).toBe("languages-python-master");
  });

  it("awards ALL tier ladders (one per axis-value) that pass", () => {
    // Python at 600h → master; Vim at 30h → apprentice. Different tierKeys.
    const catalog: LabelSpec[] = [
      ...tierLabels({
        axis: "languages",
        value: "python",
        rank: 100,
        thresholds: { novice: 5, apprentice: 20, adept: 100, master: 500, legend: 2000 },
      }),
      ...tierLabels({
        axis: "editors",
        value: "vim",
        rank: 100,
        thresholds: { novice: 5, apprentice: 20, adept: 100, master: 500, legend: 2000 },
      }),
    ];
    const awards = evaluate(
      p({ languages: [rs("python", 600)], editors: [rs("vim", 30)] }),
      { catalog },
    );
    expect(awards.map((a) => a.id).sort()).toEqual([
      "editors-vim-apprentice",
      "languages-python-master",
    ]);
  });
});

describe("evaluate — rank sort", () => {
  it("sorts awards by rank desc, then id asc for stability", () => {
    const catalog: LabelSpec[] = [
      {
        id: "low",
        kind: "archetype",
        label: "LOW",
        description: "",
        rank: 10,
        condition: { kind: "daily-avg", op: ">=", hours: 0 },
      },
      {
        id: "high",
        kind: "archetype",
        label: "HIGH",
        description: "",
        rank: 90,
        condition: { kind: "daily-avg", op: ">=", hours: 0 },
      },
      {
        id: "mid-a",
        kind: "archetype",
        label: "MID_A",
        description: "",
        rank: 50,
        condition: { kind: "daily-avg", op: ">=", hours: 0 },
      },
      {
        id: "mid-b",
        kind: "archetype",
        label: "MID_B",
        description: "",
        rank: 50,
        condition: { kind: "daily-avg", op: ">=", hours: 0 },
      },
    ];
    const awards = evaluate(p({ dailyAvg: 1 }), { catalog });
    expect(awards.map((a) => a.id)).toEqual(["high", "mid-a", "mid-b", "low"]);
  });
});

describe("evaluate — archetype non-dedupe", () => {
  it("awards multiple archetypes when several conditions hold", () => {
    const catalog: LabelSpec[] = [
      {
        id: "machine",
        kind: "archetype",
        label: "MACHINE",
        description: "",
        rank: 80,
        condition: { kind: "daily-avg", op: ">=", hours: 3 },
      },
      {
        id: "polyglot",
        kind: "archetype",
        label: "POLYGLOT",
        description: "",
        rank: 85,
        condition: {
          kind: "distinct-count",
          axis: "languages",
          minHoursEach: 5,
          op: ">=",
          n: 2,
        },
      },
    ];
    const payload = p({
      dailyAvg: 4 * 3600,
      languages: [rs("py", 10), rs("go", 10)],
    });
    const awards = evaluate(payload, { catalog });
    expect(awards.map((a) => a.id).sort()).toEqual(["machine", "polyglot"]);
  });
});
