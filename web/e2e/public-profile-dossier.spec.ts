import { expect, test } from "@playwright/test";
import { NO_STACK_REASON, stackReachableFromEnv } from "./helpers";

// gaka-k2p + oklch fix — public dossier visual pass.
//
// The /p/:slug route is UNAUTHENTICATED and renders the corpo-dossier
// visual layer. This spec targets `pandax` as the default fixture user
// (override via BOOMTIME_E2E_PUBLIC_SLUG); if the slug isn't public we
// skip the whole describe so a fresh env doesn't fail.
//
// The dossier classification banner is scoped under `.theme-arasaka`.
// Setting `theme:name` to `"arasaka"` in localStorage BEFORE the page
// mounts activates it.
//
// Coverage:
//   * Dossier classification banner ("▓ CLEARANCE: PUBLIC · SUBJECT: …")
//     renders at the top under the arasaka theme.
//   * Amber CLASSIFIED chip present.
//   * Widget corner brackets: the arasaka theme paints L-shape brackets
//     onto every `.catalyst-grid-tile` via ::before/::after gradients.
//     Assert loosely — pseudo-elements aren't queryable, so we check
//     that at least one tile has a non-empty computed ::before background.
//   * Font: hero title uses Chakra Petch (fallback: JetBrains Mono +
//     ui-monospace) so getComputedStyle().fontFamily contains one of them.
//   * LabelChip tooltip: hovering a chip pops a Portal-mounted card that
//     contains an <img data-testid="label-image"> at the large 256px
//     preview size (or the fallback glyph). We skip THIS assertion if
//     the fixture has no awarded labels.
//   * Heatmap: the activity-heatmap widget renders an SVG whose <rect>
//     elements have real (non-empty-floor) fills — proves the oklch color
//     fix is landing computed colors that d3 accepts.

const PUBLIC_SLUG = process.env.BOOMTIME_E2E_PUBLIC_SLUG ?? "pandax";

test.describe("gaka-k2p — public dossier at /p/:slug", () => {
  test.skip(!stackReachableFromEnv(), NO_STACK_REASON);

  let profileExists = false;
  test.beforeAll(async ({ request }) => {
    // Probe the public JSON endpoint. If it 404s the whole describe
    // skips — this spec is fixture-dependent and can't fabricate one.
    const res = await request.get(
      `/api/public/profile/${encodeURIComponent(PUBLIC_SLUG)}`,
    );
    profileExists = res.ok();
  });

  test.beforeEach(async ({ page }) => {
    test.skip(
      !profileExists,
      `no public profile at /p/${PUBLIC_SLUG} — set BOOMTIME_E2E_PUBLIC_SLUG or enable one`,
    );

    // Seed the arasaka theme BEFORE the SPA mounts, so
    // CatalystProvider picks it up on first render and the dossier
    // banner is not display:none.
    await page.context().clearCookies();
    await page.addInitScript(() => {
      localStorage.setItem("theme:name", JSON.stringify("arasaka"));
      localStorage.setItem("theme:variant", JSON.stringify("dark"));
    });

    await page.goto(`/p/${PUBLIC_SLUG}`);
    // Public dashboard mounts the hero title with data-testid="public-username".
    await expect(page.getByTestId("public-username")).toBeVisible({
      timeout: 15_000,
    });
  });

  test("dossier classification banner + amber CLASSIFIED chip render", async ({
    page,
  }) => {
    // The classification banner is `.public-dashboard__classline`. Under
    // the arasaka theme it flips from display:none → display:flex.
    const classline = page.locator(".public-dashboard__classline");
    await expect(classline).toBeVisible({ timeout: 10_000 });
    await expect(classline).toContainText(/CLEARANCE: PUBLIC/i);
    await expect(classline).toContainText(/SUBJECT:/i);

    // Amber CLASSIFIED chip — a `.public-dashboard__classline-stamp`
    // element with background: var(--arasaka-amber) ≈ rgb(245, 166, 35).
    const chip = page.locator(".public-dashboard__classline-stamp");
    await expect(chip).toBeVisible();
    await expect(chip).toContainText(/CLASSIFIED/i);
    const bg = await chip.evaluate(
      (el) => getComputedStyle(el).backgroundColor,
    );
    expect(bg).toMatch(/rgba?\(\s*245\s*,\s*166\s*,\s*35/);
  });

  test("hero title uses Chakra Petch / JetBrains Mono", async ({ page }) => {
    // Hero title is `<h1 class="public-dashboard__hero-title">`. It has
    // `data-testid="public-username"` for direct access.
    const title = page.getByTestId("public-username");
    const font = await title.evaluate(
      (el) => getComputedStyle(el).fontFamily,
    );
    // Under arasaka: Chakra Petch. Under other themes: JetBrains Mono
    // fallback. Accept either — this asserts the aesthetic font stack
    // reached the element vs the raw system default.
    expect(font).toMatch(/Chakra Petch|JetBrains Mono|ui-monospace|monospace/i);
  });

  test("widget tiles have corner brackets (::before/::after content)", async ({
    page,
  }) => {
    // ::before/::after aren't in the DOM tree; probe via getComputedStyle
    // on a candidate tile. The arasaka.css rule sets a `content: ''` +
    // non-empty background on `.theme-arasaka .catalyst-grid-tile::before`.
    // If the class name changes, fall back to any generic grid tile.
    const anyTile = page.locator(
      ".catalyst-grid-tile, .react-grid-item, [data-testid='hero-identity']",
    );
    await expect(anyTile.first()).toBeVisible({ timeout: 10_000 });

    const beforeStyle = await anyTile.first().evaluate((el) => {
      const s = getComputedStyle(el, "::before");
      return {
        content: s.content,
        background: s.backgroundImage || s.background,
      };
    });
    // Either content is a non-empty string OR background carries a
    // gradient — both signal an active pseudo-element paint pass.
    const hasCorner =
      (beforeStyle.content && beforeStyle.content !== "none") ||
      (beforeStyle.background && beforeStyle.background !== "none");
    expect(hasCorner).toBeTruthy();
  });

  test("LabelChip tooltip renders the 256px preview image (when awards exist)", async ({
    page,
  }) => {
    // Only meaningful when the fixture user has ≥1 awarded label. Skip
    // gracefully if no chip is on the page.
    const chip = page.getByTestId("label-chip").first();
    const chipCount = await page.getByTestId("label-chip").count();
    test.skip(
      chipCount === 0,
      "fixture user has no awarded labels — nothing to hover",
    );

    await chip.scrollIntoViewIfNeeded();
    await chip.hover();

    // The tooltip content is portaled to the body. Assert it appears
    // and contains an <img data-testid="label-image"> at the large size.
    const tooltip = page.getByTestId("label-chip-tooltip");
    await expect(tooltip).toBeVisible({ timeout: 5_000 });
    // The big image OR its fallback lives inside; at 256px it's the
    // dominant visual. The <img>'s width attr is 256 when the URL
    // resolves; when it 404s, the fallback glyph renders instead. Accept
    // either — the load-order isn't important, only that the tooltip is
    // NOT rendering the old tiny 72px thumbnail layout.
    const imageOrFallback = await tooltip.evaluate((el) => {
      const img = el.querySelector('img[data-testid="label-image"]');
      if (img instanceof HTMLImageElement) {
        return {
          kind: "img",
          w: img.width || Number(img.getAttribute("width") ?? 0),
        };
      }
      const bigFallback = el.querySelector('div[aria-hidden="true"]');
      return { kind: "fallback", height: bigFallback?.clientHeight ?? 0 };
    });
    if (imageOrFallback.kind === "img") {
      // 256px is the design target; allow shrinkage from CSS constraints
      // but require it to be markedly larger than the old 72px thumbnail.
      expect(imageOrFallback.w).toBeGreaterThanOrEqual(96);
    } else {
      // The fallback square is designed at h-64 (16rem = 256px).
      expect(imageOrFallback.height).toBeGreaterThanOrEqual(96);
    }
  });

  test("heatmap widget renders non-empty cells (oklch fix)", async ({
    page,
  }) => {
    // The default public layout places `activity-heatmap` at
    // (x=0, y=5, w=12, h=3). The rendered SVG lives inside its tile.
    // Find any SVG that contains ≥1 <rect> whose fill isn't the
    // "empty floor" color. The oklch bug rendered every cell in the
    // empty-floor fill (d3.interpolateRgb returned null on oklch inputs),
    // so proving ≥1 real fill catches a regression cleanly.
    const rects = page.locator("svg rect");
    // Wait for the first widget to render.
    await expect(rects.first()).toBeVisible({ timeout: 15_000 });

    const distinctFills = await page.evaluate(() => {
      const rs = Array.from(document.querySelectorAll("svg rect"));
      const fills = new Set<string>();
      for (const r of rs) {
        const f =
          (r as SVGRectElement).getAttribute("fill") ||
          getComputedStyle(r as Element).fill;
        if (f) fills.add(f);
      }
      return Array.from(fills);
    });
    // If the oklch fix broke, every cell would share one flat fill.
    // The heatmap AND at least one other viz should produce ≥2 distinct
    // fills across the dashboard.
    expect(distinctFills.length).toBeGreaterThanOrEqual(2);
  });
});
