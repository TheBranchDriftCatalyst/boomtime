import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { createRequire } from "node:module";
import { describe, expect, it } from "vitest";

/**
 * Regression guard for the transparent-overlay class of bugs:
 *
 * Tailwind v4's JIT only scans files in this project's own tree by default;
 * classes that live only inside catalyst-ui's compiled sources
 * (`bg-popover`, `bg-background`, `bg-primary`, etc.) will silently drop out
 * of the CSS bundle if the integration seam stops pointing Tailwind at
 * catalyst-ui. The visible symptom is dropdowns / dialogs / sheets rendering
 * transparent — the DOM is there, the styles aren't.
 *
 * Since 2.5.0 the wiring lives in `@thebranchdriftcatalyst/catalyst-ui/setup`
 * (a single stylesheet consumers @import). This test verifies BOTH ends of
 * the seam so silent regressions on either side surface loudly:
 *
 *  - `index.css` must import the setup entry (or otherwise wire @source +
 *    @theme itself).
 *  - The shipped `setup.css` must actually contain the `@source` hint AND
 *    the `@theme inline` bridge for the theme tokens.
 */

const require = createRequire(import.meta.url);

describe("boomtime → catalyst-ui integration seam", () => {
  const indexCss = readFileSync(
    resolve(__dirname, "..", "index.css"),
    "utf8",
  );

  it("imports catalyst-ui/setup (or hand-rolls the same wiring)", () => {
    // The 2.5.0+ pattern is a single `@import "@thebranchdriftcatalyst/catalyst-ui/setup"`.
    // Older versions inlined an `@theme inline` block directly. Accept either.
    const importsSetup = /@import\s+["']@thebranchdriftcatalyst\/catalyst-ui\/setup["']/.test(
      indexCss,
    );
    const hasInlineTheme = /@theme\s+inline\s*\{/.test(indexCss);
    expect(
      importsSetup || hasInlineTheme,
      "index.css must either @import '@thebranchdriftcatalyst/catalyst-ui/setup' or contain its own @theme inline block",
    ).toBe(true);
  });

  it("setup.css from the installed package contains the token bridge", () => {
    // Resolve setup.css through node's own resolver so we exercise the same
    // path Vite / Tailwind will use at build time. This catches breakages
    // where the package was published without setup.css, OR the file was
    // shipped but the exports map doesn't route "./setup" correctly.
    const setupPath = require.resolve(
      "@thebranchdriftcatalyst/catalyst-ui/setup",
    );
    const setupCss = readFileSync(setupPath, "utf8");

    expect(setupCss).toMatch(/@theme\s+inline\s*\{/);
    expect(setupCss).toMatch(/--color-primary:\s*var\(--primary\)/);
    expect(setupCss).toMatch(/--color-background:\s*var\(--background\)/);
    expect(setupCss).toMatch(/--color-popover:\s*var\(--popover\)/);
    // The @source hint targeting the library's own dist — required so the
    // JIT scans compiled component sources for utility classes. Since
    // catalyst-ui 2.5.1 (a7d08d5) this is `@source "."` — resolved relative
    // to setup.css inside dist/, it scans the whole compiled catalyst-ui
    // tree. Accept either the legacy explicit pattern or the current `"."`.
    expect(setupCss).toMatch(
      /@source\s+["'](?:[^"']*catalyst-ui[^"']*|\.)["']/,
    );
  });
});
