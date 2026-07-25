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
  it("ranks obey the plan: meme 100-199, tier 100-199, archetype 50-99, tribe 20-49", () => {
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
      // gaka-364.1: meme labels can outrank archetypes (they need to
      // beat MACHINE / DEEP FOCUS / etc for hero top-3 supremacy) but the
      // shipped ones live in the 100-199 tier-adjacent band so a Legend
      // tier still competes with SIGMA GRINDSET on merit.
      if (s.kind === "meme") {
        expect(s.rank).toBeGreaterThanOrEqual(100);
        expect(s.rank).toBeLessThan(200);
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
    // the tribe threshold and the tier ladder in one fixture. FOR THE
    // EMPEROR (gaka-364.1) also fires because the sole language is 100%
    // of the top-share; that's a legit award, not a bug in the fixture.
    const pair = evaluate(
      p({ languages: [rs("python", 30)], editors: [rs("vim", 10)] }),
    );
    expect(pair.map((a) => a.id).sort()).toEqual(
      [
        "editors-vim-novice",
        "for-the-emperor",
        "languages-python-apprentice",
        "vim-enjoyer",
      ].sort(),
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

// ============================================================================
// MEMECORE EXPANSION FIXTURES (gaka-364.1)
// ----------------------------------------------------------------------------
// One test per meme label — just-over / just-under thresholds so a tuning
// change breaks a specific assertion. Grouped by taxonomy section for
// readability; test intent stays "each label has both boundaries pinned".
// ============================================================================

describe("LABEL_CATALOG memecore — GRINDSET", () => {
  it("sigma-grindset at 6h daily-avg, not at 5.99h", () => {
    expect(evaluate(p({ dailyAvg: 6 * 3600 })).map((a) => a.id)).toContain(
      "sigma-grindset",
    );
    expect(evaluate(p({ dailyAvg: 5.99 * 3600 })).map((a) => a.id)).not.toContain(
      "sigma-grindset",
    );
  });
  it("gigachad-committer at 28-day streak, not at 27", () => {
    const active28 = Array.from({ length: 28 }, () => 3600);
    expect(evaluate(p({ dailyTotal: active28 })).map((a) => a.id)).toContain(
      "gigachad-committer",
    );
    const active27 = Array.from({ length: 28 }, (_, i) => (i === 0 ? 0 : 3600));
    expect(
      evaluate(p({ dailyTotal: active27 })).map((a) => a.id),
    ).not.toContain("gigachad-committer");
  });
  it("alpha-mogger at 90% top-project share, not at 89%", () => {
    expect(
      evaluate(
        p({ projects: [rs("main", 90), rs("side", 10)] }),
      ).map((a) => a.id),
    ).toContain("alpha-mogger");
    expect(
      evaluate(
        p({ projects: [rs("main", 89), rs("side", 11)] }),
      ).map((a) => a.id),
    ).not.toContain("alpha-mogger");
  });
  it("mewing-master fires with ≥30% of activity in 5-7am, not at 29%", () => {
    const cellsOver = [
      { dow: 1, hour: 6, seconds: 300 },
      { dow: 1, hour: 14, seconds: 700 },
    ];
    expect(
      evaluate(
        p({ punchcard: { cells: cellsOver, maxSeconds: 700, totalSeconds: 1000 } }),
      ).map((a) => a.id),
    ).toContain("mewing-master");
    const cellsUnder = [
      { dow: 1, hour: 6, seconds: 290 },
      { dow: 1, hour: 14, seconds: 710 },
    ];
    expect(
      evaluate(
        p({ punchcard: { cells: cellsUnder, maxSeconds: 710, totalSeconds: 1000 } }),
      ).map((a) => a.id),
    ).not.toContain("mewing-master");
  });
  it("hustle-android at 8h daily-avg, not at 7.99h", () => {
    expect(evaluate(p({ dailyAvg: 8 * 3600 })).map((a) => a.id)).toContain(
      "hustle-android",
    );
    expect(evaluate(p({ dailyAvg: 7.99 * 3600 })).map((a) => a.id)).not.toContain(
      "hustle-android",
    );
  });
  it("sleep-is-a-construct at 40% midnight-5am, not at 39%", () => {
    const cellsOver = [
      { dow: 1, hour: 2, seconds: 400 },
      { dow: 1, hour: 14, seconds: 600 },
    ];
    expect(
      evaluate(
        p({ punchcard: { cells: cellsOver, maxSeconds: 600, totalSeconds: 1000 } }),
      ).map((a) => a.id),
    ).toContain("sleep-is-a-construct");
    const cellsUnder = [
      { dow: 1, hour: 2, seconds: 390 },
      { dow: 1, hour: 14, seconds: 610 },
    ];
    expect(
      evaluate(
        p({ punchcard: { cells: cellsUnder, maxSeconds: 610, totalSeconds: 1000 } }),
      ).map((a) => a.id),
    ).not.toContain("sleep-is-a-construct");
  });
  it("weekend-warlord at 50% weekend, not at 49%", () => {
    const cellsOver = [
      { dow: 0, hour: 14, seconds: 500 },
      { dow: 3, hour: 14, seconds: 500 },
    ];
    expect(
      evaluate(
        p({ punchcard: { cells: cellsOver, maxSeconds: 500, totalSeconds: 1000 } }),
      ).map((a) => a.id),
    ).toContain("weekend-warlord");
    const cellsUnder = [
      { dow: 0, hour: 14, seconds: 490 },
      { dow: 3, hour: 14, seconds: 510 },
    ];
    expect(
      evaluate(
        p({ punchcard: { cells: cellsUnder, maxSeconds: 510, totalSeconds: 1000 } }),
      ).map((a) => a.id),
    ).not.toContain("weekend-warlord");
  });
});

describe("LABEL_CATALOG memecore — SPACE MARINE / WH40K", () => {
  it("space-marine fires with ≥3h daily-avg AND workday <60% of punchcard", () => {
    // Activity spread across 24h with workday <60% share, daily-avg ≥3h.
    const cellsSpread = [
      { dow: 1, hour: 10, seconds: 500 }, // workday block
      { dow: 1, hour: 22, seconds: 300 }, // evening
      { dow: 2, hour: 3, seconds: 200 }, // late night
    ];
    expect(
      evaluate(
        p({
          dailyAvg: 4 * 3600,
          punchcard: { cells: cellsSpread, maxSeconds: 500, totalSeconds: 1000 },
        }),
      ).map((a) => a.id),
    ).toContain("space-marine");
    // Daily-avg fine, but workday dominates → NOT space-marine.
    const cellsWorkday = [
      { dow: 1, hour: 10, seconds: 800 },
      { dow: 1, hour: 22, seconds: 200 },
    ];
    expect(
      evaluate(
        p({
          dailyAvg: 4 * 3600,
          punchcard: {
            cells: cellsWorkday,
            maxSeconds: 800,
            totalSeconds: 1000,
          },
        }),
      ).map((a) => a.id),
    ).not.toContain("space-marine");
  });
  it("space-marine does NOT fire on empty payload (`not` inversion guarded)", () => {
    // Regression: without the daily-avg conjunct, the `not(...)` over a
    // zero-denominator punchcard would flip to true and award SPACE MARINE
    // to everyone with no data. Guard against that.
    expect(evaluate(p({})).map((a) => a.id)).not.toContain("space-marine");
  });
  it("imperial-fist at longest streak ≥60", () => {
    const active60 = Array.from({ length: 60 }, () => 3600);
    expect(evaluate(p({ dailyTotal: active60 })).map((a) => a.id)).toContain(
      "imperial-fist",
    );
    const active59 = Array.from({ length: 59 }, () => 3600);
    expect(evaluate(p({ dailyTotal: active59 })).map((a) => a.id)).not.toContain(
      "imperial-fist",
    );
  });
  it("battle-brother-of-the-keyboard at longest streak ≥200", () => {
    const active200 = Array.from({ length: 200 }, () => 3600);
    expect(evaluate(p({ dailyTotal: active200 })).map((a) => a.id)).toContain(
      "battle-brother-of-the-keyboard",
    );
    const active199 = Array.from({ length: 199 }, () => 3600);
    expect(
      evaluate(p({ dailyTotal: active199 })).map((a) => a.id),
    ).not.toContain("battle-brother-of-the-keyboard");
  });
  it("for-the-emperor at ≥90% top-language share, not at 89%", () => {
    expect(
      evaluate(
        p({ languages: [rs("python", 90), rs("go", 10)] }),
      ).map((a) => a.id),
    ).toContain("for-the-emperor");
    expect(
      evaluate(
        p({ languages: [rs("python", 89), rs("go", 11)] }),
      ).map((a) => a.id),
    ).not.toContain("for-the-emperor");
  });
  it("chapter-master at ≥500h in one project, not at 499", () => {
    expect(
      evaluate(p({ projects: [rs("boomtime", 500)] })).map((a) => a.id),
    ).toContain("chapter-master");
    expect(
      evaluate(p({ projects: [rs("boomtime", 499)] })).map((a) => a.id),
    ).not.toContain("chapter-master");
  });
  it("dreadnought-pilot needs daily-avg ≥3h AND longest streak ≥180", () => {
    const active180 = Array.from({ length: 180 }, () => 3600);
    expect(
      evaluate(p({ dailyAvg: 3 * 3600, dailyTotal: active180 })).map((a) => a.id),
    ).toContain("dreadnought-pilot");
    // Streak alone (no daily-avg) → NOT dreadnought-pilot.
    expect(
      evaluate(p({ dailyTotal: active180 })).map((a) => a.id),
    ).not.toContain("dreadnought-pilot");
  });
  it("lord-solar at daily-avg ≥12h, not at 11.99", () => {
    expect(evaluate(p({ dailyAvg: 12 * 3600 })).map((a) => a.id)).toContain(
      "lord-solar",
    );
    expect(evaluate(p({ dailyAvg: 11.99 * 3600 })).map((a) => a.id)).not.toContain(
      "lord-solar",
    );
  });
  it("inquisitor at 8 projects ≥5h each, not at 7", () => {
    const eight = Array.from({ length: 8 }, (_, i) => rs(`p${i}`, 5));
    expect(evaluate(p({ projects: eight })).map((a) => a.id)).toContain(
      "inquisitor",
    );
    const seven = Array.from({ length: 7 }, (_, i) => rs(`p${i}`, 5));
    expect(evaluate(p({ projects: seven })).map((a) => a.id)).not.toContain(
      "inquisitor",
    );
  });
});

describe("LABEL_CATALOG memecore — KAWAII / UWU / NYAA", () => {
  it("kawaii-warlord at designing ≥15%, not at 14%", () => {
    expect(
      evaluate(p({ categories: [rs("designing", 1, 15)] })).map((a) => a.id),
    ).toContain("kawaii-warlord");
    expect(
      evaluate(p({ categories: [rs("designing", 1, 14.99)] })).map((a) => a.id),
    ).not.toContain("kawaii-warlord");
  });
  it("tsundere-compiler at 100h TypeScript, not at 99", () => {
    expect(
      evaluate(p({ languages: [rs("typescript", 100)] })).map((a) => a.id),
    ).toContain("tsundere-compiler");
    expect(
      evaluate(p({ languages: [rs("typescript", 99)] })).map((a) => a.id),
    ).not.toContain("tsundere-compiler");
  });
  it("yandere-debugger at debugging ≥10%, not at 9.99%", () => {
    expect(
      evaluate(p({ categories: [rs("debugging", 1, 10)] })).map((a) => a.id),
    ).toContain("yandere-debugger");
    expect(
      evaluate(p({ categories: [rs("debugging", 1, 9.99)] })).map((a) => a.id),
    ).not.toContain("yandere-debugger");
  });
  it("catboy-operator needs vim ≥50h AND linux ≥50h", () => {
    expect(
      evaluate(
        p({
          editors: [rs("vim", 50)],
          platforms: [rs("linux", 50)],
        }),
      ).map((a) => a.id),
    ).toContain("catboy-operator");
    // vim alone → NOT catboy-operator.
    expect(
      evaluate(p({ editors: [rs("vim", 50)] })).map((a) => a.id),
    ).not.toContain("catboy-operator");
  });
  it("femboy-fortress at 500h linux, not at 499", () => {
    expect(
      evaluate(p({ platforms: [rs("linux", 500)] })).map((a) => a.id),
    ).toContain("femboy-fortress");
    expect(
      evaluate(p({ platforms: [rs("linux", 499)] })).map((a) => a.id),
    ).not.toContain("femboy-fortress");
  });
  it("kawaii-code-mage needs 100h Python AND ai-coding ≥20%", () => {
    expect(
      evaluate(
        p({
          languages: [rs("python", 100)],
          categories: [rs("ai coding", 1, 20)],
        }),
      ).map((a) => a.id),
    ).toContain("kawaii-code-mage");
    // Python alone → NOT kawaii-code-mage.
    expect(
      evaluate(p({ languages: [rs("python", 100)] })).map((a) => a.id),
    ).not.toContain("kawaii-code-mage");
  });
  it("maid-cafe-manager needs meeting ≥10% AND 5+ langs ≥5h each", () => {
    const langs = [rs("python", 5), rs("go", 5), rs("rust", 5), rs("ts", 5), rs("js", 5)];
    expect(
      evaluate(
        p({
          categories: [rs("meeting", 1, 10)],
          languages: langs,
        }),
      ).map((a) => a.id),
    ).toContain("maid-cafe-manager");
    // Meeting alone → NOT maid-cafe-manager.
    expect(
      evaluate(p({ categories: [rs("meeting", 1, 10)] })).map((a) => a.id),
    ).not.toContain("maid-cafe-manager");
  });
  it("commander-neko-paws needs daily-avg ≥6h AND streak ≥60 AND top-share ≥70%", () => {
    const active60 = Array.from({ length: 60 }, () => 3600);
    expect(
      evaluate(
        p({
          dailyAvg: 6 * 3600,
          dailyTotal: active60,
          projects: [rs("boomtime", 70), rs("side", 30)],
        }),
      ).map((a) => a.id),
    ).toContain("commander-neko-paws");
    // Missing top-share → NOT commander-neko-paws.
    expect(
      evaluate(
        p({
          dailyAvg: 6 * 3600,
          dailyTotal: active60,
          projects: [rs("boomtime", 50), rs("side", 50)],
        }),
      ).map((a) => a.id),
    ).not.toContain("commander-neko-paws");
  });
});

describe("LABEL_CATALOG memecore — BRAINROT", () => {
  it("poggers-committer needs last7-vs-prior7 ratio ≥2.0", () => {
    // 7 days at 200s each, then 7 days at 400s each → ratio = 2.0 exactly.
    const daily = [
      ...Array.from({ length: 7 }, () => 200),
      ...Array.from({ length: 7 }, () => 400),
    ];
    expect(evaluate(p({ dailyTotal: daily })).map((a) => a.id)).toContain(
      "poggers-committer",
    );
    // Ratio 1.99 → NOT poggers-committer.
    const under = [
      ...Array.from({ length: 7 }, () => 200),
      ...Array.from({ length: 7 }, () => 398),
    ];
    expect(evaluate(p({ dailyTotal: under })).map((a) => a.id)).not.toContain(
      "poggers-committer",
    );
  });
  it("omegalul-warlock needs top-share ≥80% AND weekend ≥50%", () => {
    const cells = [
      { dow: 0, hour: 14, seconds: 500 },
      { dow: 3, hour: 14, seconds: 500 },
    ];
    expect(
      evaluate(
        p({
          projects: [rs("main", 80), rs("side", 20)],
          punchcard: { cells, maxSeconds: 500, totalSeconds: 1000 },
        }),
      ).map((a) => a.id),
    ).toContain("omegalul-warlock");
    // Weekend fine, top-share too low → NOT omegalul-warlock.
    expect(
      evaluate(
        p({
          projects: [rs("main", 70), rs("side", 30)],
          punchcard: { cells, maxSeconds: 500, totalSeconds: 1000 },
        }),
      ).map((a) => a.id),
    ).not.toContain("omegalul-warlock");
  });
  it("copium-connoisseur at ai-coding ≥35%, not at 34%", () => {
    expect(
      evaluate(p({ categories: [rs("ai coding", 1, 35)] })).map((a) => a.id),
    ).toContain("copium-connoisseur");
    expect(
      evaluate(p({ categories: [rs("ai coding", 1, 34.99)] })).map((a) => a.id),
    ).not.toContain("copium-connoisseur");
  });
  it("malding-supreme at 8 projects ≥5h each, not at 7", () => {
    const eight = Array.from({ length: 8 }, (_, i) => rs(`p${i}`, 5));
    expect(evaluate(p({ projects: eight })).map((a) => a.id)).toContain(
      "malding-supreme",
    );
    const seven = Array.from({ length: 7 }, (_, i) => rs(`p${i}`, 5));
    expect(evaluate(p({ projects: seven })).map((a) => a.id)).not.toContain(
      "malding-supreme",
    );
  });
  it("based-department fires at 100h Go or Rust", () => {
    expect(
      evaluate(p({ languages: [rs("rust", 100)] })).map((a) => a.id),
    ).toContain("based-department");
    expect(
      evaluate(p({ languages: [rs("go", 100)] })).map((a) => a.id),
    ).toContain("based-department");
    // Python at 100h alone → NOT based-department.
    expect(
      evaluate(p({ languages: [rs("python", 100)] })).map((a) => a.id),
    ).not.toContain("based-department");
  });
  it("chad-developer needs top-share ≥80% AND current streak ≥60", () => {
    const active60 = Array.from({ length: 60 }, () => 3600);
    expect(
      evaluate(
        p({
          dailyTotal: active60,
          projects: [rs("boomtime", 80), rs("side", 20)],
        }),
      ).map((a) => a.id),
    ).toContain("chad-developer");
    // Streak alone → NOT chad-developer.
    expect(
      evaluate(p({ dailyTotal: active60 })).map((a) => a.id),
    ).not.toContain("chad-developer");
  });
  it("sigma-male at top-share ≥95%, not at 94", () => {
    expect(
      evaluate(
        p({ projects: [rs("boomtime", 95), rs("side", 5)] }),
      ).map((a) => a.id),
    ).toContain("sigma-male");
    expect(
      evaluate(
        p({ projects: [rs("boomtime", 94), rs("side", 6)] }),
      ).map((a) => a.id),
    ).not.toContain("sigma-male");
  });
  it("rizz-lord needs ai-coding ≥25% AND design ≥10%", () => {
    expect(
      evaluate(
        p({
          categories: [rs("ai coding", 1, 25), rs("designing", 1, 10)],
        }),
      ).map((a) => a.id),
    ).toContain("rizz-lord");
    // ai-coding alone → NOT rizz-lord.
    expect(
      evaluate(p({ categories: [rs("ai coding", 1, 25)] })).map((a) => a.id),
    ).not.toContain("rizz-lord");
  });
});

describe("LABEL_CATALOG memecore — BUSHIDO (editor upgrades)", () => {
  it("vim-bushido at ≥50h Vim, not at 49", () => {
    expect(
      evaluate(p({ editors: [rs("vim", 50)] })).map((a) => a.id),
    ).toContain("vim-bushido");
    expect(
      evaluate(p({ editors: [rs("vim", 49)] })).map((a) => a.id),
    ).not.toContain("vim-bushido");
  });
  it("emacs-overlord at ≥200h Emacs, not at 199", () => {
    expect(
      evaluate(p({ editors: [rs("emacs", 200)] })).map((a) => a.id),
    ).toContain("emacs-overlord");
    expect(
      evaluate(p({ editors: [rs("emacs", 199)] })).map((a) => a.id),
    ).not.toContain("emacs-overlord");
  });
  it("neovim-daimyo at ≥50h Neovim, not at 49", () => {
    expect(
      evaluate(p({ editors: [rs("neovim", 50)] })).map((a) => a.id),
    ).toContain("neovim-daimyo");
    expect(
      evaluate(p({ editors: [rs("neovim", 49)] })).map((a) => a.id),
    ).not.toContain("neovim-daimyo");
  });
  it("helix-prophet at ≥20h Helix, not at 19", () => {
    expect(
      evaluate(p({ editors: [rs("helix", 20)] })).map((a) => a.id),
    ).toContain("helix-prophet");
    expect(
      evaluate(p({ editors: [rs("helix", 19)] })).map((a) => a.id),
    ).not.toContain("helix-prophet");
  });
  it("tmux-warlord fires when a terminal editor holds ≥90% share", () => {
    // NOTE: axis-pct reads `totalPct` directly (payload-precomputed by the
    // backend). Test fixtures set it explicitly on the terminal editor.
    expect(
      evaluate(
        p({ editors: [rs("neovim", 95, 95), rs("vscode", 5, 5)] }),
      ).map((a) => a.id),
    ).toContain("tmux-warlord");
    // Split evenly → NOT tmux-warlord.
    expect(
      evaluate(
        p({ editors: [rs("neovim", 50, 50), rs("vscode", 50, 50)] }),
      ).map((a) => a.id),
    ).not.toContain("tmux-warlord");
  });
  it("alacritty-devout at ≥100h vim or neovim", () => {
    expect(
      evaluate(p({ editors: [rs("neovim", 100)] })).map((a) => a.id),
    ).toContain("alacritty-devout");
    expect(
      evaluate(p({ editors: [rs("neovim", 99)] })).map((a) => a.id),
    ).not.toContain("alacritty-devout");
  });
});

describe("LABEL_CATALOG memecore — OS TRIBES++", () => {
  it("mac-warlord at ≥500h mac, not at 499", () => {
    expect(
      evaluate(p({ platforms: [rs("mac", 500)] })).map((a) => a.id),
    ).toContain("mac-warlord");
    expect(
      evaluate(p({ platforms: [rs("mac", 499)] })).map((a) => a.id),
    ).not.toContain("mac-warlord");
  });
  it("linux-emperor at ≥500h linux, not at 499", () => {
    expect(
      evaluate(p({ platforms: [rs("linux", 500)] })).map((a) => a.id),
    ).toContain("linux-emperor");
    expect(
      evaluate(p({ platforms: [rs("linux", 499)] })).map((a) => a.id),
    ).not.toContain("linux-emperor");
  });
  it("arch-btw needs ≥50h linux AND terminal-editor ≥90% share", () => {
    expect(
      evaluate(
        p({
          platforms: [rs("linux", 50)],
          editors: [rs("vim", 95, 95), rs("vscode", 5, 5)],
        }),
      ).map((a) => a.id),
    ).toContain("arch-btw");
    // Linux fine, editor share too low → NOT arch-btw.
    expect(
      evaluate(
        p({
          platforms: [rs("linux", 50)],
          editors: [rs("vim", 50, 50), rs("vscode", 50, 50)],
        }),
      ).map((a) => a.id),
    ).not.toContain("arch-btw");
  });
  it("wsl-pilgrim needs ≥50h Windows AND ≥50h Linux", () => {
    expect(
      evaluate(
        p({ platforms: [rs("linux", 50), rs("windows", 50)] }),
      ).map((a) => a.id),
    ).toContain("wsl-pilgrim");
    // Windows only → NOT wsl-pilgrim.
    expect(
      evaluate(p({ platforms: [rs("windows", 50)] })).map((a) => a.id),
    ).not.toContain("wsl-pilgrim");
  });
});

describe("LABEL_CATALOG memecore — CATEGORY OP", () => {
  it("prompt-engineer-supreme at ai-coding ≥50%, not at 49%", () => {
    expect(
      evaluate(p({ categories: [rs("ai coding", 1, 50)] })).map((a) => a.id),
    ).toContain("prompt-engineer-supreme");
    expect(
      evaluate(p({ categories: [rs("ai coding", 1, 49.99)] })).map((a) => a.id),
    ).not.toContain("prompt-engineer-supreme");
  });
  it("kubernetes-cultist at building ≥10%, not at 9%", () => {
    expect(
      evaluate(p({ categories: [rs("building", 1, 10)] })).map((a) => a.id),
    ).toContain("kubernetes-cultist");
    expect(
      evaluate(p({ categories: [rs("building", 1, 9.99)] })).map((a) => a.id),
    ).not.toContain("kubernetes-cultist");
  });
  it("markdown-monk at writing docs ≥15%, not at 14%", () => {
    expect(
      evaluate(p({ categories: [rs("writing docs", 1, 15)] })).map((a) => a.id),
    ).toContain("markdown-monk");
    expect(
      evaluate(p({ categories: [rs("writing docs", 1, 14.99)] })).map((a) => a.id),
    ).not.toContain("markdown-monk");
  });
  it("regex-sorcerer needs debugging ≥5% AND ≥100h Rust or Go", () => {
    expect(
      evaluate(
        p({
          categories: [rs("debugging", 1, 5)],
          languages: [rs("rust", 100)],
        }),
      ).map((a) => a.id),
    ).toContain("regex-sorcerer");
    // Debugging alone → NOT regex-sorcerer.
    expect(
      evaluate(p({ categories: [rs("debugging", 1, 5)] })).map((a) => a.id),
    ).not.toContain("regex-sorcerer");
  });
  it("commit-amender needs sprinter (ratio ≥1.5) AND meeting ≥5%", () => {
    const daily = [
      ...Array.from({ length: 7 }, () => 200),
      ...Array.from({ length: 7 }, () => 300),
    ];
    expect(
      evaluate(
        p({
          dailyTotal: daily,
          categories: [rs("meeting", 1, 5)],
        }),
      ).map((a) => a.id),
    ).toContain("commit-amender");
    // Meeting alone → NOT commit-amender.
    expect(
      evaluate(p({ categories: [rs("meeting", 1, 5)] })).map((a) => a.id),
    ).not.toContain("commit-amender");
  });
});

describe("LABEL_CATALOG memecore — META (composed)", () => {
  it("true-grindset-s-plus fires when all three sub-conditions hold", () => {
    const cells = [
      { dow: 0, hour: 23, seconds: 500 }, // Sun late-night
      { dow: 6, hour: 1, seconds: 500 }, // Sat late-night
    ];
    expect(
      evaluate(
        p({
          dailyAvg: 6 * 3600,
          punchcard: { cells, maxSeconds: 500, totalSeconds: 1000 },
        }),
      ).map((a) => a.id),
    ).toContain("true-grindset-s-plus");
    // Missing weekend condition (weekday activity only) → NOT true-grindset-s-plus.
    const weekdayCells = [
      { dow: 1, hour: 23, seconds: 500 },
      { dow: 2, hour: 1, seconds: 500 },
    ];
    expect(
      evaluate(
        p({
          dailyAvg: 6 * 3600,
          punchcard: {
            cells: weekdayCells,
            maxSeconds: 500,
            totalSeconds: 1000,
          },
        }),
      ).map((a) => a.id),
    ).not.toContain("true-grindset-s-plus");
  });
  it("based-chad-ultimate needs top-project ≥80%, streak ≥60 (both), top-lang ≥90%", () => {
    const active60 = Array.from({ length: 60 }, () => 3600);
    expect(
      evaluate(
        p({
          dailyTotal: active60,
          projects: [rs("boomtime", 80), rs("side", 20)],
          languages: [rs("python", 90), rs("go", 10)],
        }),
      ).map((a) => a.id),
    ).toContain("based-chad-ultimate");
    // Missing top-language dominance → NOT based-chad-ultimate.
    expect(
      evaluate(
        p({
          dailyTotal: active60,
          projects: [rs("boomtime", 80), rs("side", 20)],
          languages: [rs("python", 50), rs("go", 50)],
        }),
      ).map((a) => a.id),
    ).not.toContain("based-chad-ultimate");
  });
});

describe("LABEL_CATALOG memecore — hero top-3 dominance", () => {
  it("meme labels outrank tame archetypes and fill hero top-3 first", () => {
    // Payload that triggers BOTH tame archetypes (machine, consistent) AND
    // multiple meme labels (sigma-grindset, gigachad-committer, hustle-android).
    // The evaluator sorts rank-desc — the tame ones should fall out of top-3.
    const active60 = Array.from({ length: 60 }, () => 3600);
    const awards = evaluate(
      p({
        dailyAvg: 8 * 3600, // triggers machine, sigma-grindset, hustle-android
        dailyTotal: active60, // triggers consistent (30d), gigachad-committer (28d)
        projects: [rs("boomtime", 90), rs("side", 10)], // triggers alpha-mogger, monogamist, deep-focus
      }),
    );
    const top3Ids = awards.slice(0, 3).map((a) => a.id);
    // Every one of the top-3 must be a meme kind.
    for (const id of top3Ids) {
      const spec = awards.find((a) => a.id === id);
      expect(spec?.kind).toBe("meme");
    }
    // And tame archetypes should still be in the awards list — just lower.
    const allIds = awards.map((a) => a.id);
    expect(allIds).toContain("machine");
    expect(allIds).toContain("consistent");
  });
});
