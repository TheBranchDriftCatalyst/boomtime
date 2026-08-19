// LabelChip smoke tests. Focused on the tooltip contract (renders label,
// exposes description, renders image trigger) — Radix Tooltip mount details
// are covered by the primitive's own tests upstream.
import { describe, it, expect } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { TooltipProvider } from "@thebranchdriftcatalyst/catalyst-ui/ui/tooltip";
import { LabelChip } from "@shared/features/publicprofile/labels/LabelChip";
import type { LabelAward } from "@shared/features/publicprofile/labels/types";

const AWARD: LabelAward = {
  id: "late-night-coder",
  kind: "archetype",
  label: "LATE NIGHT CODER",
  glyph: "🌙",
  description: "≥25% of activity between 10pm and 3am",
  rank: 80,
};

function renderChip(props: Partial<{ size: "sm" | "md"; award: LabelAward }> = {}) {
  return render(
    <TooltipProvider delayDuration={0}>
      <LabelChip award={props.award ?? AWARD} size={props.size} />
    </TooltipProvider>,
  );
}

describe("LabelChip", () => {
  it("renders the label text in the chip trigger", () => {
    renderChip();
    // Trigger chip is always visible without hover
    expect(screen.getByText("LATE NIGHT CODER")).toBeInTheDocument();
  });

  it("carries the label id for data-driven selectors", () => {
    renderChip();
    const chip = screen.getByTestId("label-chip");
    expect(chip.getAttribute("data-label-id")).toBe("late-night-coder");
  });

  it("shows the tooltip description on focus (keyboard reveal path)", async () => {
    renderChip();
    const chip = screen.getByTestId("label-chip");
    // Radix opens tooltip on focus for keyboard users; this exercise mirrors
    // that path without requiring pointerEvent setup in jsdom.
    fireEvent.focus(chip);
    const tooltip = await screen.findByTestId("label-chip-tooltip");
    expect(tooltip).toBeInTheDocument();
    // Description text lands inside the tooltip content — search within it
    // so we don't collide with anything the chip trigger renders.
    expect(tooltip.textContent).toMatch(/≥25% of activity between 10pm and 3am/);
    // The full label appears in the tooltip header too.
    expect(tooltip.textContent).toContain("LATE NIGHT CODER");
  });

  it("shows tier when the award carries one (tooltip footer)", async () => {
    cleanup();
    renderChip({
      award: {
        ...AWARD,
        id: "python-adept",
        label: "PYTHON ADEPT",
        kind: "tier",
        tier: "adept",
        description: "100h+ in Python",
      },
    });
    fireEvent.focus(screen.getByTestId("label-chip"));
    const tooltip = await screen.findByTestId("label-chip-tooltip");
    expect(tooltip.textContent).toMatch(/tier/i);
    expect(tooltip.textContent).toMatch(/adept/i);
  });

  it("uses smaller glyph size for size='sm' (hero tagline density)", () => {
    renderChip({ size: "sm" });
    const chip = screen.getByTestId("label-chip");
    // sm padding class is px-1.5 py-0.5 vs md px-2 py-1
    expect(chip.className).toContain("px-1.5");
    expect(chip.className).toContain("py-0.5");
  });
});
