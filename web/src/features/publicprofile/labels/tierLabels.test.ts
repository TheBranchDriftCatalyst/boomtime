// tierLabels.test.ts — the tier ladder helper must expand into 5
// specs with correct ids, tiers, tierKey collisions, rank bonuses, and
// threshold conditions.
import { describe, expect, it } from "vitest";
import { tierLabels } from "./tierLabels";

describe("tierLabels", () => {
  const specs = tierLabels({
    axis: "languages",
    value: "python",
    glyph: "🐍",
    rank: 100,
    thresholds: { novice: 5, apprentice: 20, adept: 100, master: 500, legend: 2000 },
  });

  it("emits exactly 5 specs (one per tier)", () => {
    expect(specs).toHaveLength(5);
    expect(specs.map((s) => s.tier)).toEqual([
      "novice",
      "apprentice",
      "adept",
      "master",
      "legend",
    ]);
  });

  it("all 5 specs share the same tierKey for evaluator dedupe", () => {
    const keys = new Set(specs.map((s) => s.tierKey));
    expect(keys.size).toBe(1);
    expect(specs[0].tierKey).toBe("languages:python");
  });

  it("rank increases monotonically legend > master > … > novice", () => {
    const ranks = specs.map((s) => s.rank);
    expect(ranks).toEqual([100, 101, 102, 103, 104]);
  });

  it("legend spec's condition carries the highest threshold", () => {
    const legend = specs.find((s) => s.tier === "legend")!;
    expect(legend.condition).toEqual({
      kind: "axis-time",
      axis: "languages",
      value: "python",
      op: ">=",
      hours: 2000,
    });
  });

  it("default label template uppercases value + tier", () => {
    expect(specs.find((s) => s.tier === "master")!.label).toBe("PYTHON MASTER");
  });

  it("value case is normalized in the id (lowercase) even if caller passes mixed case", () => {
    const specs2 = tierLabels({
      axis: "editors",
      value: "VimBoss",
      rank: 100,
      thresholds: { novice: 1, apprentice: 2, adept: 3, master: 4, legend: 5 },
    });
    expect(specs2[0].id).toBe("editors-vimboss-novice");
    expect(specs2[0].tierKey).toBe("editors:vimboss");
  });

  it("respects a custom labelTemplate", () => {
    const specs3 = tierLabels({
      axis: "languages",
      value: "rust",
      rank: 100,
      thresholds: { novice: 1, apprentice: 2, adept: 3, master: 4, legend: 5 },
      labelTemplate: "{tier} of {value}",
    });
    expect(specs3.find((s) => s.tier === "adept")!.label).toBe("ADEPT of RUST");
  });
});
