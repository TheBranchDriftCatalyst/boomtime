// HeroIdentity.test.tsx — hero-identity now derives its tagline from
// the label evaluator (gaka-364). The old hard-coded
// "{TOP_LANG}-CLASS · {TOP_EDITOR}-ADEPT" template is gone.
//
// Invariants under test:
//   - EMPTY PAYLOAD: no awards → tagline reads "NEW OPERATOR" (better
//     signal than the old POLYGLOT-CLASS placeholder).
//   - RICH PAYLOAD: tagline shows the top-3 award labels as LabelChips
//     in rank-desc order (previously joined by "·"; gaka-mem-chip made
//     each award its own hover-tooltip'd chip).
//   - USERNAME still renders regardless.
import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TooltipProvider } from "@thebranchdriftcatalyst/catalyst-ui/ui/tooltip";
import { WidgetRenderer } from "./WidgetRenderer";
import type { PublicDashboardPayload } from "@/types/stats";
import { LABEL_CATALOG } from "@/features/publicprofile/labels/catalog";
import { qk } from "@/lib/queryKeys";
import type { LabelSpec } from "@/features/publicprofile/labels/types";

// LabelChip requires a TooltipProvider ancestor (Radix contract). The app
// mounts one at the root in main.tsx; tests wrap here explicitly.
//
// Post gaka-364.3 the hero also needs a QueryClientProvider — useLabelsCatalog
// fetches the DB catalog via react-query. We seed the cache with LABEL_CATALOG
// (converted to the DB wire shape) so the hero renders synchronously without
// hitting the network in tests.
function renderHero(data: PublicDashboardPayload) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  });
  // Prime the cache with the shipped catalog. Shape must match the wire
  // response: {systemPrompt, labels: [LabelCatalogRow]}. We fake a
  // LabelCatalogRow from each LabelSpec — the evaluator only reads
  // condition + kind + rank + tier so the extra fields can be blank.
  qc.setQueryData(qk.labelsCatalog(), {
    systemPrompt: "",
    labels: (LABEL_CATALOG as LabelSpec[]).map((s) => ({
      id: s.id,
      kind: s.kind,
      label: s.label,
      glyph: s.glyph ?? "",
      description: s.description,
      optimizedPrompt: s.imagePrompt ?? "",
      rank: s.rank,
      tier: s.tier ?? "",
      condition: s.condition,
      createdAt: "",
      updatedAt: "",
    })),
  });
  return render(
    <QueryClientProvider client={qc}>
      <TooltipProvider delayDuration={0}>
        <WidgetRenderer kind="hero-identity" data={data} />
      </TooltipProvider>
    </QueryClientProvider>,
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

const rs = (name: string, hours: number, pct?: number) => ({
  name,
  totalSeconds: hours * 3600,
  totalPct: pct ?? 0,
  totalDaily: [],
  pctDaily: [],
});

describe("HeroIdentity tagline", () => {
  it("shows NEW OPERATOR fallback on empty payload", () => {
    renderHero(p({}));
    expect(screen.getByTestId("hero-tagline")).toHaveTextContent("NEW OPERATOR");
  });

  it("shows top-3 awards as LabelChips on a rich payload", () => {
    // gaka-364.1: memecore labels (kind:"meme", rank 100-199) outrank the
    // tame archetypes so the hero surfaces the OP names first.
    // Same rank-tiebreak expectation as before — just checking the labels
    // land as separate LabelChip nodes now instead of a text join.
    const daily = Array.from({ length: 30 }, () => 3 * 3600);
    const data = p({
      languages: [rs("python", 500)],
      editors: [rs("vim", 500)],
      dailyAvg: 3 * 3600,
      dailyTotal: daily,
    });
    renderHero(data);
    const tagline = screen.getByTestId("hero-tagline");
    const chips = tagline.querySelectorAll('[data-testid="label-chip"]');
    expect(chips).toHaveLength(3);
    // All expected labels should be present, order enforced by rank+tiebreak
    const labels = Array.from(chips).map((c) => c.textContent?.trim());
    expect(labels).toEqual([
      "GIGACHAD COMMITTER",
      "SPACE MARINE",
      "FOR THE EMPEROR",
    ]);
  });

  it("renders username regardless of award state", () => {
    renderHero(p({ username: "zorak" }));
    // Username shows twice: as "> PROFILE · zorak@boomtime" and as the big header
    expect(screen.getAllByText(/zorak/i).length).toBeGreaterThanOrEqual(1);
  });

  // gaka-mem-chip: chips carry their own <img> via LabelImage. In tests
  // the images 404 (no backend), but the <img> elements are in the DOM
  // before onError triggers, so we can assert src is wired to
  // /api/v1/labels/{id}/image.
  it("each chip in the tagline carries an <img> pointing at the label image endpoint", () => {
    const data = p({
      languages: [rs("python", 500)],
      editors: [rs("vim", 500)],
      dailyAvg: 3 * 3600,
      dailyTotal: Array.from({ length: 30 }, () => 3 * 3600),
    });
    renderHero(data);
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
    renderHero(p({}));
    expect(screen.queryAllByTestId("label-chip")).toHaveLength(0);
  });
});
