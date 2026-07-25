// LabelsShowcase.test.tsx — the labels-showcase dashboard widget.
// Invariants:
//   - EMPTY: no awards → renders the "NO LABELS YET" placeholder
//     (matches the plan's empty-state fallback).
//   - GROUPED: awards render in three sections in order tier →
//     archetype → tribe; each section header shows the count.
//   - AWARD METADATA: every award chip carries the label text, glyph
//     if present, and a title tooltip = description.
//
// These render against the SHIPPED catalog on purpose — regressions in
// either the catalog or the evaluator will be visible.
import { describe, expect, it } from "vitest";
import { render, screen, within, fireEvent } from "@testing-library/react";
import { TooltipProvider } from "@thebranchdriftcatalyst/catalyst-ui/ui/tooltip";
import { LabelsShowcase } from "./LabelsShowcase";
import type { PublicDashboardPayload } from "@/types/stats";

// LabelChip requires TooltipProvider ancestor; app root mounts one, tests
// wrap explicitly.
function renderShowcase(data: PublicDashboardPayload) {
  return render(
    <TooltipProvider delayDuration={0}>
      <LabelsShowcase data={data} />
    </TooltipProvider>,
  );
}

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

describe("LabelsShowcase", () => {
  it("renders the empty-state placeholder when no labels are awarded", () => {
    renderShowcase(p({}));
    expect(screen.getByText(/NO LABELS YET/i)).toBeInTheDocument();
  });

  it("renders a rich payload with all three sections", () => {
    // Payload seeded to hit at least one label in each category:
    //   tier: python-master (500h)
    //   archetype: machine (3h daily avg)
    //   tribe: vim-enjoyer (10h vim)
    const data = p({
      languages: [rs("python", 500)],
      editors: [rs("vim", 10)],
      dailyAvg: 3 * 3600,
      dailyTotal: [3 * 3600],
    });
    renderShowcase(data);

    // All three section headers present
    expect(screen.getByText("Tiers")).toBeInTheDocument();
    expect(screen.getByText("Archetypes")).toBeInTheDocument();
    expect(screen.getByText("Tribes")).toBeInTheDocument();

    // Specific known awards render as chips
    const chips = screen.getAllByTestId("label-chip");
    const ids = chips.map((c) => c.getAttribute("data-label-id"));
    expect(ids).toContain("languages-python-master");
    expect(ids).toContain("machine");
    expect(ids).toContain("vim-enjoyer");
  });

  it("each awarded chip opens a Radix tooltip with the label description on focus", async () => {
    // gaka-mem-chip: previously used the native `title` attribute; now uses
    // catalyst-ui Tooltip (Radix). Focus the chip to trigger tooltip mount.
    const data = p({ dailyAvg: 3 * 3600 });
    renderShowcase(data);
    const machine = screen.getByText("MACHINE").closest('[data-testid="label-chip"]');
    expect(machine).not.toBeNull();
    fireEvent.focus(machine!);
    const tooltip = await screen.findByTestId("label-chip-tooltip");
    expect(tooltip.textContent).toMatch(/daily average/i);
  });

  it("hides sections that have no awards", () => {
    // vim-only payload → no tier fires (10h → editors-vim-novice is tier=novice, so tier section WILL fire)
    // Use a payload that only produces a tribe: 200h mac → mac-native tribe, but no tier since
    // there's no "mac" editor tier. Actually 200h on mac platform, and no language/editor time.
    const data = p({ platforms: [rs("mac", 200)] });
    renderShowcase(data);
    expect(screen.queryByText("Tiers")).not.toBeInTheDocument();
    expect(screen.queryByText("Archetypes")).not.toBeInTheDocument();
    expect(screen.getByText("Tribes")).toBeInTheDocument();
    // exactly one tribe chip
    const tribes = screen.getAllByTestId("label-chip");
    expect(tribes).toHaveLength(1);
    expect(within(tribes[0]).getByText("MAC NATIVE")).toBeInTheDocument();
  });

  // gaka-myv: every chip now includes a <LabelImage> that resolves to
  // /api/v1/labels/<id>/image with a glyph fallback on 404. The test can't
  // hit a live backend, so we assert the <img> element exists inside each
  // chip and its src is wired correctly.
  it("renders a LabelImage inside every award chip", () => {
    const data = p({ platforms: [rs("mac", 200)] });
    renderShowcase(data);
    const chip = screen.getByTestId("label-chip");
    const img = chip.querySelector('img[data-testid="label-image"]') as HTMLImageElement | null;
    expect(img).not.toBeNull();
    expect(img!.getAttribute("src")).toBe("/api/v1/labels/mac-native/image");
    expect(img!.getAttribute("data-label-id")).toBe("mac-native");
  });
});
