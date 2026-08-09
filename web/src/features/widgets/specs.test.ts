// specs.test.ts — the FE-side twin of internal/widget/spec_test.go's
// cross-language guard. Both sides read the SAME internal/widget/specs.json
// (see specs.ts's alias-based import), so this test isn't re-parsing a
// second copy — it's guarding that:
//   1. every WIDGET_CATALOG kind (catalog.ts) has a spec entry, and
//   2. the hand-maintained SVG_RENDERABLE_KINDS set (catalog.ts) matches
//      EXACTLY the set of specs.json entries with target === "both".
// #2 is the important one: SVG_RENDERABLE_KINDS is what the Embeddable
// Widgets panel filters against, and it's still a hand-maintained Set (not
// derived from specs.json, to avoid an invasive catalog.ts rewrite this
// stage) — this test is the drift guard that keeps it honest, mirroring how
// internal/widget/render_test.go's TestKindsMatchFrontendCatalog guards
// Kinds() against a hardcoded FE-catalog mirror in the other direction.
import { describe, expect, it } from "vitest";
import { SVG_RENDERABLE_KINDS, WIDGET_CATALOG } from "./catalog";
import { specs } from "./specs";

describe("specs.json mirrors the FE catalog", () => {
  it("every WIDGET_CATALOG kind has a spec entry", () => {
    const specKinds = new Set(specs.map((s) => s.kind));
    for (const entry of WIDGET_CATALOG) {
      expect(specKinds.has(entry.kind), `missing spec entry for ${entry.kind}`).toBe(true);
    }
  });

  it("specs.json has no kind outside the catalog", () => {
    const catalogKinds = new Set(WIDGET_CATALOG.map((e) => e.kind));
    for (const s of specs) {
      expect(catalogKinds.has(s.kind), `spec kind ${s.kind} is not in WIDGET_CATALOG`).toBe(true);
    }
  });

  it("SVG_RENDERABLE_KINDS equals the set of specs.json entries with target === 'both'", () => {
    const bothKinds = new Set(specs.filter((s) => s.target === "both").map((s) => s.kind));
    expect(bothKinds).toEqual(SVG_RENDERABLE_KINDS);
  });

  it("every fe-only spec carries a reason and no panels", () => {
    for (const s of specs) {
      if (s.target !== "fe-only") continue;
      expect(s.reason, `${s.kind}: fe-only spec is missing a reason`).toBeTruthy();
      expect(s.panels ?? [], `${s.kind}: fe-only spec should not declare panels`).toHaveLength(0);
    }
  });

  it("every both spec carries at least one panel", () => {
    for (const s of specs) {
      if (s.target !== "both") continue;
      expect(s.panels?.length ?? 0, `${s.kind}: both spec has no panels`).toBeGreaterThan(0);
    }
  });
});
