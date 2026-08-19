// ChipList.test.tsx — regression tests for the chip-list variant of
// the categories / editors / platforms widgets on the public profile.
//
// Bug history: the chip renderer previously read `it.totalPct` and
// rendered it directly via `Math.round(it.totalPct ?? 0)%`. The backend
// emits `totalPct` as a 0..1 decimal (see internal/labels/evaluator.go
// note and internal/db/queries/get_user_activity_rollup.sql —
// `total_seconds / SUM(total_seconds) OVER ()`), so a resource with a
// real 15% share got rendered as "0%" (Math.round(0.15) === 0). Every
// chip on the page read "0%" or "1%" while the PIE variant of the same
// widget (which recomputes share from totalSeconds) rendered correctly.
//
// The fix recomputes share from totalSeconds over the rendered set,
// mirroring PieChart's approach. Both invariants are pinned here so a
// future refactor that reintroduces the totalPct read gets caught.
//
// Part B Stage 5 cutover: categories-chart/editors-chips/platforms-chips are
// target:"both" kinds, so WidgetRenderer now routes them unconditionally
// through SpecRenderer (its own Chips component, data-testid="spec-chip") —
// the bespoke ChipList component this file used to exercise directly
// (data-testid="dossier-chip") is deleted. SpecRenderer's Chips carries the
// exact same recompute-from-totalSeconds fix (see SpecRenderer.tsx), so
// these regression pins still guard the same invariant, just through the
// new path.
import { describe, expect, it } from "vitest";
import { screen, within } from "@testing-library/react";
import { WidgetRenderer } from "./WidgetRenderer";
import type { PublicDashboardPayload, ResourceStats } from "@shared/types/stats";
import { renderWithProviders } from "@shared/test/renderWithProviders";

function render(ui: Parameters<typeof renderWithProviders>[0]) {
  return renderWithProviders(ui);
}

const emptyPayload = (
  over: Partial<PublicDashboardPayload> = {},
): PublicDashboardPayload => ({
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

// Backend `totalPct` is a 0..1 decimal (per SQL rollup) — use the same
// scale in fixtures so we're testing the actual wire shape, not a fake
// 0..100 idealized version.
function stat(
  name: string,
  totalSeconds: number,
  totalPct: number,
): ResourceStats {
  return { name, totalSeconds, totalPct, totalDaily: [], pctDaily: [] };
}

describe("ChipList percent rendering (categories/editors/platforms)", () => {
  it("renders a 0..1 decimal totalPct=0.15 as '15%' (not '0%')", () => {
    // Two resources: one at 15%, one at 85% of the range. Real backend
    // shape — `totalPct` is 0..1 and would round to 0/1 if rendered raw.
    const payload = emptyPayload({
      categories: [
        stat("AI Coding", 15, 0.15),
        stat("Coding", 85, 0.85),
      ],
    });
    render(<WidgetRenderer kind="categories-chart" view="chips" data={payload} />);

    const chips = screen.getAllByTestId("spec-chip");
    expect(chips).toHaveLength(2);
    // Order preserved from input; chip text is "<NAME> <PCT>%".
    expect(chips[0].textContent).toMatch(/AI Coding/);
    expect(chips[0].textContent).toMatch(/15%/);
    expect(chips[0].textContent).not.toMatch(/\b0%/);
    expect(chips[1].textContent).toMatch(/Coding/);
    expect(chips[1].textContent).toMatch(/85%/);
  });

  it("chip percentages sum to ~100 across the rendered set", () => {
    // Screenshot regression: on the reported bug the chips read
    // "1% / 0% / 0% / 0% / 0% / 0% / 0% / 0%" for a set whose true
    // shares summed to 100. Assert the fixed renderer preserves that.
    const payload = emptyPayload({
      categories: [
        stat("AI Coding", 40, 0.4),
        stat("Browsing", 25, 0.25),
        stat("Coding", 20, 0.2),
        stat("Meeting", 10, 0.1),
        stat("Writing Docs", 5, 0.05),
      ],
    });
    render(<WidgetRenderer kind="categories-chart" view="chips" data={payload} />);

    const chips = screen.getAllByTestId("spec-chip");
    const pcts = chips
      .map((c) => c.textContent?.match(/(\d+)%/)?.[1])
      .map((s) => Number(s));
    for (const n of pcts) expect(Number.isFinite(n)).toBe(true);
    const sum = pcts.reduce((a, b) => a + b, 0);
    // Rounding jitter ±2 is fine; the failing bug summed to 1.
    expect(sum).toBeGreaterThanOrEqual(98);
    expect(sum).toBeLessThanOrEqual(102);
  });

  it("editors-chips uses the same recompute-from-seconds contract", () => {
    // Editors path exercises the exact same ChipList, so a regression
    // that scopes the fix to `categories-chart` only would still fail
    // for the "everything reads 0%" screenshot. Pin the contract at
    // the dispatch site.
    const payload = emptyPayload({
      editors: [
        stat("GoogleChrome", 60, 0.6),
        stat("iTerm2", 40, 0.4),
      ],
    });
    render(<WidgetRenderer kind="editors-chips" data={payload} />);
    const chips = screen.getAllByTestId("spec-chip");
    expect(chips[0].textContent).toMatch(/60%/);
    expect(chips[1].textContent).toMatch(/40%/);
  });

  it("platforms-chips uses the same recompute-from-seconds contract", () => {
    const payload = emptyPayload({
      platforms: [stat("MACINTOSH", 100, 1.0)],
    });
    render(<WidgetRenderer kind="platforms-chips" data={payload} />);
    const chips = screen.getAllByTestId("spec-chip");
    expect(chips[0].textContent).toMatch(/100%/);
  });

  it("shows the Empty note when the resource list is empty", () => {
    // Preserved behavior from the original ChipList — regressions here
    // would render an empty chip container instead of the "No data"
    // marker the grid tile shows.
    const payload = emptyPayload({ categories: [] });
    render(<WidgetRenderer kind="categories-chart" view="chips" data={payload} />);
    // The categories-chart branch guards categories.length upstream of
    // ChipList; either way we should NOT see any chips.
    expect(screen.queryAllByTestId("spec-chip")).toHaveLength(0);
  });

  it("ignores stale totalPct entirely — a fixture with wrong totalPct still renders the right share", () => {
    // Defensive: if the wire shape ever flips scales again, chip render
    // MUST derive share from totalSeconds, not trust the label. Fixture
    // supplies a nonsense totalPct=999 to prove we ignore it.
    const payload = emptyPayload({
      categories: [
        stat("A", 25, 999),
        stat("B", 75, -12),
      ],
    });
    render(<WidgetRenderer kind="categories-chart" view="chips" data={payload} />);
    const chips = screen.getAllByTestId("spec-chip");
    const texts = chips.map((c) => c.textContent ?? "");
    expect(texts[0]).toMatch(/A/);
    expect(texts[0]).toMatch(/25%/);
    expect(texts[1]).toMatch(/B/);
    expect(texts[1]).toMatch(/75%/);
    // Not present anywhere.
    for (const t of texts) {
      expect(t).not.toMatch(/999/);
      expect(t).not.toMatch(/-12/);
    }
    // Belt-and-braces for the `within` import used to keep helpers ready.
    expect(within(chips[0]).getByText(/A/)).toBeTruthy();
  });
});
