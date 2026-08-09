// HeroIdentity.test.tsx — hero-identity now derives its tagline from
// the server-side label evaluator (gaka-hc6.3). The old hard-coded
// "{TOP_LANG}-CLASS · {TOP_EDITOR}-ADEPT" template is gone.
//
// gaka-hc6.5: post client-eval delete, tests prime qk.awards("own")
// directly with LabelAward fixtures — no evaluator call in this file.
//
// Invariants under test:
//   - EMPTY AWARDS: tagline reads "NEW OPERATOR"
//   - RICH AWARDS: tagline shows the top-3 awards as LabelChips in
//     rank-desc order (gaka-mem-chip made each award its own chip)
//   - USERNAME still renders regardless
//   - Every chip's <img> src points at /api/v1/labels/<id>/image
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TooltipProvider } from "@thebranchdriftcatalyst/catalyst-ui/ui/tooltip";
import { WidgetRenderer } from "./WidgetRenderer";
import type { PublicDashboardPayload } from "@/types/stats";
import type { LabelAward } from "@/features/publicprofile/labels/types";
import { qk } from "@/lib/queryKeys";
import { MemoryRouter } from "react-router";

// WidgetRenderer now reads usePublicConfig() unconditionally (Part B Stage 3
// spec-engine gate); the default MSW handler for /config/public
// (handlers.ts) advertises widget_spec_engine: false so this suite keeps
// exercising the bespoke hero-identity render unaffected.
function renderHero(payload: PublicDashboardPayload, awards: LabelAward[] = []) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  qc.setQueryData(qk.awards("own"), awards);
  return render(
    <MemoryRouter>
      <QueryClientProvider client={qc}>
        <TooltipProvider delayDuration={0}>
          <WidgetRenderer kind="hero-identity" data={payload} />
        </TooltipProvider>
      </QueryClientProvider>
    </MemoryRouter>,
  );
}

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

const meme = (id: string, label: string, rank = 150): LabelAward => ({
  id,
  kind: "meme",
  label,
  description: "",
  rank,
});

describe("HeroIdentity tagline", () => {
  it("shows NEW OPERATOR fallback on empty payload", () => {
    renderHero(p({}), []);
    expect(screen.getByTestId("hero-tagline")).toHaveTextContent("NEW OPERATOR");
  });

  it("shows top-3 awards as LabelChips on a rich payload", () => {
    // gaka-364.1: memecore labels (kind:"meme") outrank tame archetypes so
    // the hero surfaces the OP names first. Same chip-count expectation as
    // before — the widget slices the first 3 out of the awards array.
    renderHero(p({}), [
      meme("gigachad-committer", "GIGACHAD COMMITTER", 180),
      meme("space-marine", "SPACE MARINE", 170),
      meme("for-the-emperor", "FOR THE EMPEROR", 160),
      // Extra awards past top-3 are not rendered in the hero
      meme("also-ran", "ALSO RAN", 100),
    ]);
    const tagline = screen.getByTestId("hero-tagline");
    const chips = tagline.querySelectorAll('[data-testid="label-chip"]');
    expect(chips).toHaveLength(3);
    const labels = Array.from(chips).map((c) => c.textContent?.trim());
    expect(labels).toEqual([
      "GIGACHAD COMMITTER",
      "SPACE MARINE",
      "FOR THE EMPEROR",
    ]);
  });

  it("renders username regardless of award state", () => {
    renderHero(p({ username: "zorak" }), []);
    expect(screen.getAllByText(/zorak/i).length).toBeGreaterThanOrEqual(1);
  });

  // gaka-mem-chip: chips carry their own <img> via LabelImage. In tests
  // the images 404 (no backend), but the <img> elements are in the DOM
  // before onError triggers, so we can assert src is wired to
  // /api/v1/labels/{id}/image.
  it("each chip in the tagline carries an <img> pointing at the label image endpoint", () => {
    renderHero(p({}), [
      meme("gigachad-committer", "GIGACHAD COMMITTER", 180),
      meme("space-marine", "SPACE MARINE", 170),
      meme("for-the-emperor", "FOR THE EMPEROR", 160),
    ]);
    const tagline = screen.getByTestId("hero-tagline");
    const imgs = Array.from(tagline.querySelectorAll("img"));
    expect(imgs.length).toBeGreaterThanOrEqual(3);
    for (const img of imgs) {
      expect(img.getAttribute("src")).toMatch(
        /^\/api\/v1\/labels\/[a-zA-Z0-9\-]+\/image$/,
      );
    }
  });

  it("renders no LabelChips when there are no awards (only NEW OPERATOR text)", () => {
    renderHero(p({}), []);
    expect(screen.queryAllByTestId("label-chip")).toHaveLength(0);
  });
});
