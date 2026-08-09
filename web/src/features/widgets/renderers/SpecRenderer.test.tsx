// SpecRenderer.test.tsx — non-tautological coverage for the Part B Stage 3
// spec-driven renderer (gaka-174.x). Three concerns:
//
//   1. Binding correctness: a panel bound to (say) "editors" must show
//      EDITOR names, never language/project/platform/category names — the
//      exact class of bug a hand-copied switch statement invites. Every
//      resource-list-bound kind gets a fixture where every list has a
//      DISTINCT, non-overlapping name so a wrong-binding regression fails
//      loudly instead of silently matching the wrong assertion.
//   2. Composite layout: multi-panel kinds (stats-card, stats-card-with-
//      grade, profile-summary, deep-work) render every one of their panels.
//   3. Guard: every target:"both" spec kind's primitives all resolve to a
//      registry entry (no kind can silently render "unsupported
//      primitive"), and no "both" kind ever falls back to the unknown-spec
//      / unsupported-primitive placeholder.
//
// jsdom has no real layout: every WIDTH-TRACKED D3 chart (PieChart,
// HeatmapChart, MomentumGrid, Punchcard, HourBarChart, CumulativeArea — see
// useD3Surface's `sizeToFrame && frame.width === 0` guard) measures 0 and
// skips its draw, so their bound VALUES aren't assertable here the way
// ContributionCalendar's are (it opts out via sizeToFrame:false — see
// ContributionCalendar.test.tsx's own note on this). Those primitives get a
// smoke assertion (mounts a real <svg>, not the Empty/Unsupported
// fallback) instead of a content assertion; the "bars" (view="bar") and
// "chips" (default view) primitives — which SpecRenderer backs with its own
// plain-DOM BarList/Chips, not a D3 surface — carry the binding-correctness
// weight instead, same as ChipList.test.tsx does for the bespoke switch.
import { describe, expect, it } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { SpecRenderer, SUPPORTED_PRIMITIVES, type SpecRenderData } from "./SpecRenderer";
import { specs } from "@/features/widgets/specs";
import { secondsToCompact } from "@/lib/utils";
import { computeGrade, currentStreak, longestStreakInRange } from "@/features/publicprofile/grade";
import type { ResourceStats } from "@/types/stats";

function stat(name: string, totalSeconds: number): ResourceStats {
  return { name, totalSeconds, totalPct: 0, totalDaily: [], pctDaily: [] };
}

// Every resource list gets a DISTINCT name so a wrong-binding regression
// (e.g. editors-chips accidentally reading data.languages) fails loudly.
function specData(over: Partial<SpecRenderData> = {}): SpecRenderData {
  return {
    totalSeconds: 8100, // 2h 15m
    dailyAvg: 3600,
    dailyTotal: [3600, 0, 7200, 3600, 0, 3600, 7200],
    startDate: "2026-07-01T00:00:00Z",
    projects: [stat("boomtime", 40000)],
    languages: [stat("TypeScript", 30000)],
    editors: [stat("VSCode", 20000)],
    platforms: [stat("macOS", 15000)],
    categories: [stat("Coding", 25000)],
    punchcard: { cells: [{ dow: 1, hour: 9, seconds: 1200 }], maxSeconds: 1200, totalSeconds: 1200 },
    momentum: {
      weeks: ["2026-06-15", "2026-06-22"],
      projects: [{ name: "boomtime", weekly: [1800, 3600], totalSeconds: 5400 }],
    },
    sessions: {
      summary: { count: 5, totalSeconds: 18000, avgSeconds: 3600, maxSeconds: 7200, medianSeconds: 3000 },
      daily: [
        { date: "2026-07-01", sessions: 2, totalSeconds: 3600, longestSeconds: 1800 },
        { date: "2026-07-02", sessions: 1, totalSeconds: 1800, longestSeconds: 1800 },
      ],
      histogram: [{ label: "0-30m", count: 2 }],
    },
    ...over,
  };
}

const BOTH_KINDS = specs.filter((s) => s.target === "both");

describe("SpecRenderer guard: every both-kind's primitives resolve", () => {
  it("every primitive named in a both-target spec has a registry entry", () => {
    expect(BOTH_KINDS.length).toBeGreaterThan(0);
    for (const spec of BOTH_KINDS) {
      for (const panel of spec.panels ?? []) {
        expect(
          SUPPORTED_PRIMITIVES.has(panel.primitive),
          `${spec.kind}: primitive "${panel.primitive}" has no SpecRenderer registry entry`,
        ).toBe(true);
      }
    }
  });

  it("no both-kind ever renders the unsupported-primitive / unknown-spec fallback", () => {
    for (const spec of BOTH_KINDS) {
      const { container, unmount } = render(
        <SpecRenderer kind={spec.kind} view="bar" data={specData()} />,
      );
      expect(container.textContent, spec.kind).not.toMatch(/Unsupported primitive/);
      expect(container.textContent, spec.kind).not.toMatch(/No spec for/);
      unmount();
    }
  });

  it("an unknown kind (no spec entry) renders the no-spec placeholder", () => {
    render(<SpecRenderer kind="not-a-real-kind" data={specData()} />);
    expect(screen.getByText(/No spec for "not-a-real-kind"/)).toBeInTheDocument();
  });

  it("a fe-only kind (no panels) also renders the no-spec placeholder — SpecRenderer never guesses at fe-only content", () => {
    const feOnly = specs.find((s) => s.target === "fe-only");
    expect(feOnly).toBeDefined();
    render(<SpecRenderer kind={feOnly!.kind} data={specData()} />);
    expect(screen.getByText(new RegExp(`No spec for "${feOnly!.kind}"`))).toBeInTheDocument();
  });
});

describe("SpecRenderer binding correctness (plain-DOM primitives)", () => {
  it("top-langs (bars/languages, bar view) shows the language name, not project/editor/platform names", () => {
    render(<SpecRenderer kind="top-langs" view="bar" data={specData()} />);
    expect(screen.getByText("TypeScript")).toBeInTheDocument();
    expect(screen.queryByText("boomtime")).not.toBeInTheDocument();
    expect(screen.queryByText("VSCode")).not.toBeInTheDocument();
  });

  it("top-projects (bars/projects, bar view) shows the project name, not the language name", () => {
    render(<SpecRenderer kind="top-projects" view="bar" data={specData()} />);
    expect(screen.getByText("boomtime")).toBeInTheDocument();
    expect(screen.queryByText("TypeScript")).not.toBeInTheDocument();
  });

  it("editors-chips (chips/editors) shows editor names, NOT language names", () => {
    render(<SpecRenderer kind="editors-chips" data={specData()} />);
    const chips = screen.getAllByTestId("spec-chip");
    expect(chips.map((c) => c.textContent)).toEqual([expect.stringContaining("VSCode")]);
    expect(screen.queryByText("TypeScript")).not.toBeInTheDocument();
  });

  it("platforms-chips (chips/platforms) shows platform names, not editor names", () => {
    render(<SpecRenderer kind="platforms-chips" data={specData()} />);
    const chips = screen.getAllByTestId("spec-chip");
    expect(chips.map((c) => c.textContent)).toEqual([expect.stringContaining("macOS")]);
    expect(screen.queryByText("VSCode")).not.toBeInTheDocument();
  });

  it("categories-chart (chips/categories, default chips view) shows category names", () => {
    render(<SpecRenderer kind="categories-chart" data={specData()} />);
    expect(screen.getByTestId("spec-chip").textContent).toContain("Coding");
  });

  it("categories-chart honors view=pie by switching primitives (smoke: mounts a chart, not the chip list)", () => {
    const { container } = render(<SpecRenderer kind="categories-chart" view="pie" data={specData()} />);
    expect(screen.queryByTestId("spec-chip")).not.toBeInTheDocument();
    expect(container.querySelector("svg")).toBeTruthy();
  });

  it("total-time-stat (stat-numeral/total-seconds) shows the compact total", () => {
    const data = specData({ totalSeconds: 8100 });
    render(<SpecRenderer kind="total-time-stat" data={data} />);
    expect(screen.getByText("TOTAL TIME")).toBeInTheDocument();
    expect(screen.getByText(secondsToCompact(8100))).toBeInTheDocument();
  });

  it("daily-avg-stat (stat-numeral/daily-avg) shows the rounded compact average", () => {
    const data = specData({ dailyAvg: 3661 });
    render(<SpecRenderer kind="daily-avg-stat" data={data} />);
    expect(screen.getByText("DAILY AVG")).toBeInTheDocument();
    expect(screen.getByText(secondsToCompact(Math.round(3661)))).toBeInTheDocument();
  });

  it("current-streak-stat vs longest-streak-stat: distinct dailyTotal shapes prove the two bindings aren't swapped", () => {
    // 3-day run, a gap, a 2-day run, a gap, a lone trailing active day:
    // longest run = 3 (the head), current (trailing) run = 1.
    const data = specData({ dailyTotal: [3600, 3600, 3600, 0, 3600, 3600, 0, 3600] });
    expect(currentStreak(data.dailyTotal)).toBe(1);
    expect(longestStreakInRange(data.dailyTotal)).toBe(3);

    const { unmount } = render(<SpecRenderer kind="current-streak-stat" data={data} />);
    expect(screen.getByText("CURRENT STREAK")).toBeInTheDocument();
    expect(screen.getByText("1D")).toBeInTheDocument();
    unmount();

    render(<SpecRenderer kind="longest-streak-stat" data={data} />);
    expect(screen.getByText("LONGEST STREAK")).toBeInTheDocument();
    expect(screen.getByText("3D")).toBeInTheDocument();
  });

  it("active-days-stat (ratio) computes active/total from dailyTotal, matching WidgetRenderer.tsx's bespoke calc", () => {
    // [3600,0,7200,3600,0,3600,7200]: 5 of 7 days active.
    render(<SpecRenderer kind="active-days-stat" data={specData()} />);
    expect(screen.getByText("ACTIVE DAYS")).toBeInTheDocument();
    expect(screen.getByText("5/7")).toBeInTheDocument();
    expect(screen.getByText("71%")).toBeInTheDocument();
  });

  it("badge renders the shields.io-style 'boomtime: <compact total>' pill", () => {
    const data = specData({ totalSeconds: 8100 });
    render(<SpecRenderer kind="badge" data={data} />);
    const badge = screen.getByTestId("spec-badge");
    expect(badge.textContent).toContain("boomtime");
    expect(badge.textContent).toContain(secondsToCompact(8100));
  });
});

describe("SpecRenderer composite (multi-panel) kinds", () => {
  it("stats-card: two metric tiles (Total / Daily avg) + a bars sub-panel bound to languages", () => {
    const data = specData();
    render(<SpecRenderer kind="stats-card" view="bar" data={data} />);
    const composite = screen.getByTestId("spec-composite");
    expect(within(composite).getAllByTestId("spec-panel-metric")).toHaveLength(2);
    expect(within(composite).getByTestId("spec-panel-bars")).toBeInTheDocument();
    expect(screen.getByText("Total")).toBeInTheDocument();
    expect(screen.getByText(secondsToCompact(data.totalSeconds))).toBeInTheDocument();
    expect(screen.getByText("Daily avg")).toBeInTheDocument();
    expect(screen.getByText(secondsToCompact(Math.round(data.dailyAvg)))).toBeInTheDocument();
    expect(screen.getByText("TypeScript")).toBeInTheDocument();
  });

  it("stats-card-with-grade: stats-card's panels plus a grade-ring showing the computed letter", () => {
    const data = specData();
    render(<SpecRenderer kind="stats-card-with-grade" data={data} />);
    const composite = screen.getByTestId("spec-composite");
    expect(within(composite).getByTestId("spec-panel-grade-ring")).toBeInTheDocument();
    const grade = computeGrade(data);
    expect(screen.getByTestId("spec-grade-ring-letter").textContent).toBe(grade.level);
  });

  it("profile-summary: all 5 panels present (calendar, bars, grade-ring, 2 metrics)", () => {
    render(<SpecRenderer kind="profile-summary" data={specData()} />);
    const composite = screen.getByTestId("spec-composite");
    expect(within(composite).getByTestId("spec-panel-calendar")).toBeInTheDocument();
    expect(within(composite).getByTestId("spec-panel-bars")).toBeInTheDocument();
    expect(within(composite).getByTestId("spec-panel-grade-ring")).toBeInTheDocument();
    expect(within(composite).getAllByTestId("spec-panel-metric")).toHaveLength(2);
  });

  it("deep-work: 3 metric tiles from the sessions summary + an area panel", () => {
    const data = specData();
    render(<SpecRenderer kind="deep-work" data={data} />);
    expect(screen.getByText("Sessions")).toBeInTheDocument();
    expect(screen.getByText(String(data.sessions!.summary.count))).toBeInTheDocument();
    expect(screen.getByText("Median length")).toBeInTheDocument();
    expect(screen.getByText(secondsToCompact(data.sessions!.summary.medianSeconds))).toBeInTheDocument();
    expect(screen.getByText("Longest")).toBeInTheDocument();
    expect(screen.getByText(secondsToCompact(data.sessions!.summary.maxSeconds))).toBeInTheDocument();
    expect(screen.getByTestId("spec-panel-area")).toBeInTheDocument();
  });

  it("deep-work degrades to zero/empty when sessions is absent (Overview widgets pass this before the query resolves)", () => {
    const data = specData({ sessions: undefined });
    render(<SpecRenderer kind="deep-work" data={data} />);
    expect(screen.getByText("Sessions")).toBeInTheDocument();
    expect(screen.getByText("0")).toBeInTheDocument(); // count field, no duration formatting
    expect(screen.getByText("No session data yet")).toBeInTheDocument();
  });
});

describe("SpecRenderer smoke coverage (width-tracked D3 primitives — see file doc)", () => {
  // These primitives measure 0 width in jsdom (useD3Surface's sizeToFrame
  // guard), so only "did the right COMPONENT mount" is assertable here, not
  // the drawn content. Binding correctness for their resolvers is still
  // exercised (resolveResources/data.momentum/data.punchcard get called),
  // just not visually re-verified in this environment.
  it.each([
    ["activity-heatmap", undefined],
    ["punchcard", undefined],
    ["punchcard", "hour-bars"],
    ["momentum", undefined],
    ["cumulative-area", undefined],
    ["heatmap-projects", undefined],
    ["heatmap-languages", undefined],
  ] as const)("%s (view=%s) mounts a real chart, not a fallback", (kind, view) => {
    const { container } = render(<SpecRenderer kind={kind} view={view} data={specData()} />);
    expect(container.querySelector("svg")).toBeTruthy();
    expect(container.textContent).not.toMatch(/No data|No spec for|Unsupported primitive/);
  });

  it("activity-heatmap (calendar/daily-total) draws one day-cell per dailyTotal entry — ContributionCalendar opts out of width-tracking so this DOES verify binding length", () => {
    const data = specData({ dailyTotal: [100, 200, 300, 400, 500] });
    const { container } = render(<SpecRenderer kind="activity-heatmap" data={data} />);
    expect(container.querySelectorAll("g.day").length).toBe(5);
  });
});
