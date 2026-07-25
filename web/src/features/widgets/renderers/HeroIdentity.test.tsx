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
    // Two masters at rank 103 outrank everything else; tie-break is id-asc.
    //   editors-vim-master (103)   ← e < l → wins tie
    //   languages-python-master (103)
    //   consistent (rank 90)       ← streak-driven archetype
    const daily = Array.from({ length: 30 }, () => 3 * 3600);
    const data = p({
      languages: [rs("python", 500)],
      editors: [rs("vim", 500)],
      dailyAvg: 3 * 3600,
      dailyTotal: daily,
    });
    render(<WidgetRenderer kind="hero-identity" data={data} />);
    const tagline = screen.getByTestId("hero-tagline");
    expect(tagline).toHaveTextContent("VIM MASTER · PYTHON MASTER · CONSISTENT");
  });

  it("renders username regardless of award state", () => {
    render(<WidgetRenderer kind="hero-identity" data={p({ username: "zorak" })} />);
    // Username shows twice: as "> PROFILE · zorak@boomtime" and as the big header
    expect(screen.getAllByText(/zorak/i).length).toBeGreaterThanOrEqual(1);
  });
});
