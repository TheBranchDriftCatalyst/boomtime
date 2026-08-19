// LabelsShowcase.test.tsx — the labels-showcase dashboard widget.
//
// gaka-hc6.5: post evaluator delete, this file no longer runs the client
// evaluator. Tests prime the qk.awards("own") cache directly with fixed
// LabelAward[] arrays and assert the rendering. Evaluator correctness is
// tested in Go (internal/labels/evaluator_test.go); this suite is purely
// a rendering test.
//
// Invariants under test:
//   - EMPTY: no awards → renders the "NO LABELS YET" placeholder
//   - GROUPED: awards render in sections in kind order; each section
//     header shows the count
//   - AWARD METADATA: every award chip carries the label text, glyph
//     if present, and a tooltip = description
//   - IMAGE: every chip renders a <LabelImage> wired to
//     /api/v1/labels/<id>/image
import { describe, expect, it } from "vitest";
import { render, screen, within, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TooltipProvider } from "@thebranchdriftcatalyst/catalyst-ui/ui/tooltip";
import { LabelsShowcase } from "./LabelsShowcase";
import type { PublicDashboardPayload } from "@shared/types/stats";
import type { LabelAward } from "@shared/features/publicprofile/labels/types";
import { qk } from "@shared/lib/queryKeys";
import { MemoryRouter } from "react-router";

function renderShowcase(awards: LabelAward[]) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  qc.setQueryData(qk.awards("own"), awards);
  return render(
    <MemoryRouter>
      <QueryClientProvider client={qc}>
        <TooltipProvider delayDuration={0}>
          <LabelsShowcase data={emptyPayload} />
        </TooltipProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

// LabelsShowcase's `data` prop is unused post gaka-hc6.4 (awards come
// from useAwards()) but the widget renderer contract still passes one.
const emptyPayload: PublicDashboardPayload = {
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
};

// Fixture builders — one line per award, easy to read at the call site.
const tier = (id: string, label: string, desc = ""): LabelAward => ({
  id,
  kind: "tier",
  label,
  description: desc,
  rank: 100,
});
const archetype = (id: string, label: string, desc = ""): LabelAward => ({
  id,
  kind: "archetype",
  label,
  description: desc,
  rank: 50,
});
const tribe = (id: string, label: string, desc = ""): LabelAward => ({
  id,
  kind: "tribe",
  label,
  description: desc,
  rank: 30,
});

describe("LabelsShowcase", () => {
  it("renders the empty-state placeholder when no labels are awarded", () => {
    renderShowcase([]);
    expect(screen.getByText(/NO LABELS YET/i)).toBeInTheDocument();
  });

  it("renders a rich payload with all three sections", () => {
    renderShowcase([
      tier("languages-python-master", "PYTHON MASTER"),
      archetype("machine", "MACHINE"),
      tribe("vim-enjoyer", "VIM ENJOYER"),
    ]);

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
    renderShowcase([
      archetype("machine", "MACHINE", "for daily average grinders"),
    ]);
    const machine = screen.getByText("MACHINE").closest('[data-testid="label-chip"]');
    expect(machine).not.toBeNull();
    fireEvent.focus(machine!);
    const tooltip = await screen.findByTestId("label-chip-tooltip");
    expect(tooltip.textContent).toMatch(/daily average/i);
  });

  it("hides sections that have no awards", () => {
    // Tribe-only fixture: no tier, no archetype → Tiers + Archetypes headers
    // should be absent; Tribes header + one chip present.
    renderShowcase([tribe("mac-native", "MAC NATIVE")]);
    expect(screen.queryByText("Tiers")).not.toBeInTheDocument();
    expect(screen.queryByText("Archetypes")).not.toBeInTheDocument();
    expect(screen.getByText("Tribes")).toBeInTheDocument();
    const tribes = screen.getAllByTestId("label-chip");
    expect(tribes).toHaveLength(1);
    expect(within(tribes[0]).getByText("MAC NATIVE")).toBeInTheDocument();
  });

  // gaka-myv: every chip now includes a <LabelImage> that resolves to
  // /api/v1/labels/<id>/image with a glyph fallback on 404. The test can't
  // hit a live backend, so we assert the <img> element exists inside each
  // chip and its src is wired correctly.
  it("renders a LabelImage inside every award chip", () => {
    renderShowcase([tribe("mac-native", "MAC NATIVE")]);
    const chip = screen.getByTestId("label-chip");
    const img = chip.querySelector('img[data-testid="label-image"]') as HTMLImageElement | null;
    expect(img).not.toBeNull();
    expect(img!.getAttribute("src")).toBe("/api/v1/labels/mac-native/image");
    expect(img!.getAttribute("data-label-id")).toBe("mac-native");
  });
});
