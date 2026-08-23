import { expect, test } from "@playwright/test";
import {
  ADMIN_PASSWORD,
  ADMIN_USERNAME,
  NO_ADMIN_CREDS_REASON,
  NO_STACK_REASON,
  loginAsAdmin,
  revokeTokensByNameSubstring,
  stackReachableFromEnv,
} from "./helpers";

// boom-swagger — 33d8c52, 15268da, 8712fa0, 13c0309, dbd7d22, 35b3153
//
// The self-hosted Swagger UI at /api/docs/ ships with:
//   * Arasaka dark theme (crimson + amber + jet-black).
//   * #boom-fab: bottom-right floating action panel. Unauth'd shows a
//     "sign in to mint tokens" link; auth'd shows an auth chip +
//     GENERATE + MANAGE token buttons.
//   * #boom-version-chip topbar chip driven by /healthz. NEVER
//     "FILE #dev · REV dev" — the 15268da fallback fix guarantees a
//     meaningful stamp.
//   * Keyboard shortcuts: `/` focuses the filter, `?` opens the help
//     modal, Escape closes any modal.
//   * docExpansion "none" — no operation block pre-expanded.
//   * Schemas rendered as data-sheet cards (dbd7d22): amber
//     border-left, corner brackets on hover.
//   * Token mint flow (auth'd): GENERATE → modal with #boom-token-value
//     UUID → AUTHORIZE HERE authorizes Swagger's own auth state.

test.describe("boom-swagger — /api/docs/ UI upgrades", () => {
  test.skip(!stackReachableFromEnv(), NO_STACK_REASON);

  // Small helper: wait for the Swagger UI to actually mount. The bundled
  // JS runs on window load and mounts SwaggerUI async — the FAB doesn't
  // appear until the /auth/users/current probe resolves either way.
  async function gotoDocsFresh(page: import("@playwright/test").Page) {
    // Drop any session cookies so we test the unauth'd default; each
    // describe-scoped test that needs auth logs in first.
    await page.context().clearCookies();
    await page.goto("/api/docs/");
    // The Swagger UI root mounts into #swagger-ui; the initializer runs
    // on window.onload and appends #boom-fab. Wait for both.
    await page.waitForSelector("#swagger-ui", { timeout: 15_000 });
    await page.waitForSelector("#boom-fab", { timeout: 15_000 });
  }

  test.describe("unauthenticated", () => {
    test("loads the dark theme + version chip + FAB with sign-in link", async ({
      page,
    }) => {
      await gotoDocsFresh(page);

      // 1. Dark theme: body background is the arasaka jet-black.
      // The Swagger initializer sets html/body background to var(--bx-bg)
      // = #08080c. Assert the RGB value ends up in getComputedStyle.
      const bodyBg = await page.evaluate(
        () => getComputedStyle(document.body).backgroundColor,
      );
      // Accept rgb(8, 8, 12) or rgba(8, 8, 12, ...) — computed values vary
      // slightly across browser versions.
      expect(bodyBg).toMatch(/rgba?\(\s*8\s*,\s*8\s*,\s*12/);

      // 2. Version chip renders in the topbar and does NOT show the
      //    "FILE #dev · REV dev" fallback (fix: 15268da).
      const versionChip = page.locator("#boom-version-chip");
      await expect(versionChip).toBeVisible({ timeout: 10_000 });
      const chipText = await versionChip.textContent();
      expect(chipText).not.toContain("FILE #dev");
      // Either a real sha stamp, a version, or the neutral DEV BUILD label.
      expect(chipText).toMatch(/^▓\s+(FILE #|v|BOOMTIME)/i);

      // 3. FAB present bottom-right; unauth'd path shows the sign-in link
      //    ("sign in to mint tokens") and NO auth chip.
      const fab = page.locator("#boom-fab");
      await expect(fab).toBeVisible();
      await expect(
        fab.locator("a", { hasText: /sign in to mint tokens/i }),
      ).toBeVisible({ timeout: 10_000 });
      await expect(fab.locator("#boom-auth-chip")).toHaveCount(0);

      // FAB always keeps the "back to app" link (styled as <a href="/">).
      await expect(fab.locator('a[href="/"]', { hasText: /back to app/i }))
        .toBeVisible();
    });

    test("docs default collapsed — no opblock is pre-expanded", async ({
      page,
    }) => {
      await gotoDocsFresh(page);

      // Wait for the specs to render at least one tag row before asserting.
      await page.waitForSelector(".swagger-ui .opblock-tag", {
        timeout: 15_000,
      });
      // docExpansion: "none" means no opblock is open. Swagger applies
      // `.is-open` to expanded elements.
      const openTagBlocks = page.locator(".swagger-ui .opblock-tag.is-open");
      await expect(openTagBlocks).toHaveCount(0);
      const openOps = page.locator(".swagger-ui .opblock.is-open");
      await expect(openOps).toHaveCount(0);
    });

    test("keyboard shortcuts focus the filter and open + close the help modal", async ({
      page,
    }) => {
      await gotoDocsFresh(page);

      // `?` opens the help modal.
      await page.keyboard.press("?");
      const helpModal = page.locator("#boom-help-modal");
      await expect(helpModal).toBeVisible({ timeout: 5_000 });

      // Escape closes it.
      await page.keyboard.press("Escape");
      await expect(helpModal).toHaveCount(0);

      // `/` focuses the topbar filter input. Swagger's filter uses the
      // `.filter-container input` selector.
      await page.keyboard.press("/");
      const focused = await page.evaluate(
        () => document.activeElement?.tagName,
      );
      expect(focused).toBe("INPUT");
    });

    test("filter input hides non-matching tag blocks", async ({ page }) => {
      await gotoDocsFresh(page);
      await page.waitForSelector(".swagger-ui .opblock-tag", {
        timeout: 15_000,
      });
      const filter = page.locator(".filter-container input");
      await filter.fill("thisIsAveryUnlikelyFilterMatch");
      // Swagger removes tag sections from the DOM on filter-no-match.
      await expect(
        page.locator(".swagger-ui .opblock-tag"),
      ).toHaveCount(0, { timeout: 10_000 });
      await filter.fill("");
    });

    test("schema section renders model-container cards with the amber border-left", async ({
      page,
    }) => {
      await gotoDocsFresh(page);

      // Swagger's models section is at the bottom; scroll into view.
      const models = page.locator(".swagger-ui section.models").first();
      await models.scrollIntoViewIfNeeded();
      await expect(models).toBeVisible({ timeout: 15_000 });

      // dbd7d22 rewrites schemas as proper data-sheet cards — each
      // .model-container has a 2px amber (#f5a623) border-left. Pick the
      // first card and verify the computed style.
      const card = models.locator(".model-container").first();
      await expect(card).toBeVisible({ timeout: 10_000 });
      const borderLeft = await card.evaluate(
        (el) => getComputedStyle(el).borderLeftColor,
      );
      // Amber ~= rgb(245, 166, 35). Some browsers return with commas + spaces.
      expect(borderLeft).toMatch(/rgba?\(\s*245\s*,\s*166\s*,\s*35/);
    });

    test("environment picker only renders when spec declares >1 server", async ({
      page,
    }) => {
      await gotoDocsFresh(page);
      // The picker only appears when spec.servers has ≥2 entries — the
      // shipped spec typically only declares "/". Conditional assertion:
      // if the picker exists, it must have at least one <option>.
      const picker = page.locator("#boom-env-picker");
      // Wait a beat for the servers-based init to run.
      await page.waitForTimeout(1500);
      const count = await picker.count();
      if (count > 0) {
        const opts = picker.locator("option");
        expect(await opts.count()).toBeGreaterThanOrEqual(1);
      }
      // If it isn't rendered, that's the expected mono-server case and we
      // don't fail — this test's job is to catch a regression that would
      // render a broken empty picker.
    });
  });

  test.describe("authenticated (admin) token flow", () => {
    test.skip(!ADMIN_USERNAME || !ADMIN_PASSWORD, NO_ADMIN_CREDS_REASON);

    test.beforeEach(async ({ page }) => {
      const ok = await loginAsAdmin(page);
      test.skip(!ok, NO_ADMIN_CREDS_REASON);
    });

    // A distinct token-name substring per run keeps parallel workers +
    // manual re-runs from stomping on each other's cleanup pass.
    const TOKEN_SUBSTR = `e2e-swagger-${Date.now()}`;

    test.afterEach(async ({ page }) => {
      await revokeTokensByNameSubstring(page, TOKEN_SUBSTR);
    });

    test("FAB renders auth chip + mint/manage buttons", async ({ page }) => {
      await page.goto("/api/docs/");
      await page.waitForSelector("#boom-fab", { timeout: 15_000 });
      const chip = page.locator("#boom-auth-chip");
      await expect(chip).toBeVisible({ timeout: 10_000 });
      await expect(chip).toContainText(ADMIN_USERNAME);

      await expect(
        page.locator("#boom-fab button", { hasText: /generate api token/i }),
      ).toBeVisible();
      await expect(
        page.locator("#boom-fab button", { hasText: /manage tokens/i }),
      ).toBeVisible();
    });

    test("mint modal, authorize wiring, cleanup", async ({ page }) => {
      await page.goto("/api/docs/");
      await page.waitForSelector("#boom-fab", { timeout: 15_000 });

      // Auto-answer the window.prompt() with a unique test-owned name so
      // afterEach can find + revoke it.
      const tokenName = `${TOKEN_SUBSTR}-mint`;
      page.once("dialog", async (d) => {
        // The mint flow calls window.prompt(); Playwright surfaces it as
        // a "prompt" dialog.
        expect(d.type()).toBe("prompt");
        await d.accept(tokenName);
      });

      await page
        .locator("#boom-fab button", { hasText: /generate api token/i })
        .click();

      const modal = page.locator("#boom-token-modal");
      await expect(modal).toBeVisible({ timeout: 15_000 });
      await expect(
        modal.locator("h3", { hasText: /NEW API TOKEN/ }),
      ).toBeVisible();

      // The minted token lives in #boom-token-value — assert a UUIDish
      // stringpresence. It's stored as base64(uuid) on the server, but the
      // wire form is opaque; we just check it looks non-empty + long.
      const tokenValue = await modal
        .locator("#boom-token-value")
        .textContent();
      expect(tokenValue?.trim().length ?? 0).toBeGreaterThan(16);

      // AUTHORIZE HERE closes the modal and wires the token into
      // Swagger's own auth state.
      await modal.locator("button", { hasText: /AUTHORIZE HERE/ }).click();
      await expect(modal).toHaveCount(0);

      // Swagger's authSelectors.authorized() returns a non-empty Map when
      // any scheme is active. Read via the injected window.ui handle.
      const authed = await page.evaluate(() => {
        // @ts-expect-error window.ui exists after the swagger bundle boots.
        const auth = window.ui.getSystem().authSelectors.authorized();
        return auth && auth.size > 0;
      });
      expect(authed).toBeTruthy();
    });
  });
});
