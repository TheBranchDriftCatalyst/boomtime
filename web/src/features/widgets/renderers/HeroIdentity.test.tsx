// HeroIdentity.test.tsx — hero-identity now derives its tagline from
// the label evaluator (gaka-364). The old hard-coded
// "{TOP_LANG}-CLASS · {TOP_EDITOR}-ADEPT" template is gone.
//
// Invariants under test:
//   - EMPTY PAYLOAD: no awards → tagline reads "NEW OPERATOR" (better
//     signal than the old POLYGLOT-CLASS placeholder).
//   - RICH PAYLOAD: tagline shows the top-3 award labels joined by "·"
//     in rank-desc order.
//   - USERNAME still renders regardless.
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { WidgetRenderer } from "./WidgetRenderer";
import type { PublicDashboardPayload } from "@/types/stats";

const p = (over: Partial<PublicDashboardPayload>): PublicDashboardPayload => ({
  username: "pandax",
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

describe("HeroIdentity tagline", () => {
  it("shows NEW OPERATOR fallback on empty payload", () => {
    render(<WidgetRenderer kind="hero-identity" data={p({})} />);
    expect(screen.getByTestId("hero-tagline")).toHaveTextContent("NEW OPERATOR");
  });

  it("shows top-3 awards joined by · on a rich payload", () => {
    // gaka-364.1: memecore labels (kind:"meme", rank 100-199) outrank the
    // tame archetypes so the hero surfaces the OP names first.
    // With python 500h + vim 500h + 30-day streak + 3h daily-avg:
    //   gigachad-committer (rank 130) — 28d+ streak
    //   space-marine       (rank 130) — 3h+ daily-avg, punchcard spread
    //   for-the-emperor    (rank 125) — top-language 100% share
    // Ties break by id-asc, so gigachad < space, and both outrank the
    // 125-ranked FOR THE EMPEROR. Vim/Python MASTER (rank 103) and CONSISTENT
    // (rank 90) still get awarded — they just don't win the hero top-3.
    const daily = Array.from({ length: 30 }, () => 3 * 3600);
    const data = p({
      languages: [rs("python", 500)],
      editors: [rs("vim", 500)],
      dailyAvg: 3 * 3600,
      dailyTotal: daily,
    });
    render(<WidgetRenderer kind="hero-identity" data={data} />);
    const tagline = screen.getByTestId("hero-tagline");
    expect(tagline).toHaveTextContent(
      "GIGACHAD COMMITTER · SPACE MARINE · FOR THE EMPEROR",
    );
  });

  it("renders username regardless of award state", () => {
    render(<WidgetRenderer kind="hero-identity" data={p({ username: "zorak" })} />);
    // Username shows twice: as "> PROFILE · zorak@boomtime" and as the big header
    expect(screen.getAllByText(/zorak/i).length).toBeGreaterThanOrEqual(1);
  });
});
