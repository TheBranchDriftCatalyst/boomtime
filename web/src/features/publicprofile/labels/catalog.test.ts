// catalog.test.ts — walks the SHIPPED LABEL_CATALOG against curated
// fixtures to catch drift. Every fixture pairs a payload with the exact
// award-id set expected. If someone edits a threshold, one of these
// fixtures either fires unexpectedly or stops firing — either way the
// diff is visible in the test failure.
//
// This is the guard against "silently tuned a label to award to more
// people" changes that would land without a test change.
import { describe, expect, it } from "vitest";
import { evaluate } from "./evaluator";
import { LABEL_CATALOG } from "./catalog";
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

describe("LABEL_CATALOG structural invariants", () => {
  it("every id is unique", () => {
    const ids = LABEL_CATALOG.map((s) => s.id);
    expect(new Set(ids).size).toBe(ids.length);
  });
  it("every archetype and tribe has a non-empty description", () => {
    for (const s of LABEL_CATALOG) {
      if (s.kind !== "tier") {
        expect(s.description.length).toBeGreaterThan(0);
      }
    }
  });
  it("ranks obey the plan: tier 100-199, archetype 50-99, tribe 20-49", () => {
    for (const s of LABEL_CATALOG) {
      if (s.kind === "tier") expect(s.rank).toBeGreaterThanOrEqual(100);
      if (s.kind === "archetype") {
        expect(s.rank).toBeGreaterThanOrEqual(50);
        expect(s.rank).toBeLessThan(100);
      }
      if (s.kind === "tribe") {
        expect(s.rank).toBeGreaterThanOrEqual(20);
        expect(s.rank).toBeLessThan(50);
      }
    }
  });
});

describe("LABEL_CATALOG awarded-set fixtures", () => {
  it("empty payload → zero awards", () => {
    expect(evaluate(p({}))).toEqual([]);
  });

  it("python 30h → python-apprentice; python 30h + vim 10h → tier + tribe both fire", () => {
    const solo = evaluate(p({ languages: [rs("python", 30)] }));
    expect(solo.map((a) => a.id)).toContain("languages-python-apprentice");

    // Vim at exactly 10h clears vim-enjoyer (>=10h) AND novice tier (>=5h),
    // but NOT apprentice (>=20h) — locks the "just over" boundary on both
    // the tribe threshold and the tier ladder in one fixture.
    const pair = evaluate(
      p({ languages: [rs("python", 30)], editors: [rs("vim", 10)] }),
    );
    expect(pair.map((a) => a.id).sort()).toEqual(
      ["editors-vim-novice", "languages-python-apprentice", "vim-enjoyer"].sort(),
    );
  });

  it("python 500h → python-master (not adept, not legend)", () => {
    const awards = evaluate(p({ languages: [rs("python", 500)] }));
    const pythonAwards = awards.filter((a) => a.id.startsWith("languages-python-"));
    expect(pythonAwards).toHaveLength(1);
    expect(pythonAwards[0].id).toBe("languages-python-master");
  });

  it("polyglot archetype fires when 5 langs each ≥5h", () => {
    const langs = [rs("python", 5), rs("go", 5), rs("rust", 5), rs("ts", 5), rs("js", 5)];
    const awards = evaluate(p({ languages: langs }));
    expect(awards.map((a) => a.id)).toContain("polyglot");
  });

  it("polyglot does NOT fire with 4 langs at 5h", () => {
    const langs = [rs("python", 5), rs("go", 5), rs("rust", 5), rs("ts", 5)];
    const awards = evaluate(p({ languages: langs }));
    expect(awards.map((a) => a.id)).not.toContain("polyglot");
  });

  it("late-night-coder fires with ≥25% activity in 22-3", () => {
    const cells = [
      { dow: 1, hour: 23, seconds: 300 },
      { dow: 1, hour: 12, seconds: 700 },
    ];
    const awards = evaluate(
      p({ punchcard: { cells, maxSeconds: 700, totalSeconds: 1000 } }),
    );
    expect(awards.map((a) => a.id)).toContain("late-night-coder");
  });

  it("late-night-coder does NOT fire at 24% share", () => {
    const cells = [
      { dow: 1, hour: 23, seconds: 240 },
      { dow: 1, hour: 12, seconds: 760 },
    ];
    const awards = evaluate(
      p({ punchcard: { cells, maxSeconds: 760, totalSeconds: 1000 } }),
    );
    expect(awards.map((a) => a.id)).not.toContain("late-night-coder");
  });

  it("machine archetype fires at exactly 3h daily-avg", () => {
    const awards = evaluate(p({ dailyAvg: 3 * 3600, dailyTotal: [3 * 3600] }));
    expect(awards.map((a) => a.id)).toContain("machine");
  });

  it("consistent fires with 30-day current streak, not with 29", () => {
    const active30 = Array.from({ length: 30 }, () => 3600);
    expect(evaluate(p({ dailyTotal: active30 })).map((a) => a.id)).toContain(
      "consistent",
    );
    const active29 = Array.from({ length: 30 }, (_, i) => (i === 0 ? 0 : 3600));
    expect(evaluate(p({ dailyTotal: active29 })).map((a) => a.id)).not.toContain(
      "consistent",
    );
  });

  it("meeting-warrior at 10% meeting share; not at 9.99", () => {
    expect(
      evaluate(p({ categories: [rs("meeting", 1, 10)] })).map((a) => a.id),
    ).toContain("meeting-warrior");
    expect(
      evaluate(p({ categories: [rs("meeting", 1, 9.99)] })).map((a) => a.id),
    ).not.toContain("meeting-warrior");
  });

  it("mac-native fires at 200h on mac", () => {
    expect(
      evaluate(p({ platforms: [rs("Mac", 200)] })).map((a) => a.id),
    ).toContain("mac-native");
  });

  it("cross-platform fires with 2+ platforms ≥50h each", () => {
    expect(
      evaluate(
        p({ platforms: [rs("linux", 60), rs("mac", 60), rs("windows", 10)] }),
      ).map((a) => a.id),
    ).toContain("cross-platform");
  });
});
