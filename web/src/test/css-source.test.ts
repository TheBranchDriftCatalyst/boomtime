import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * Regression guard for the transparent-overlay class of bugs:
 *
 * Tailwind v4's JIT only scans files in this project's own tree by default;
 * classes that live only inside catalyst-ui's compiled sources
 * (`bg-popover`, `bg-background`, `bg-primary`, etc.) will silently drop out
 * of the CSS bundle if `index.css` stops pointing Tailwind at catalyst-ui.
 * The visible symptom is dropdowns / dialogs / sheets rendering transparent
 * — the DOM is there, the styles aren't.
 *
 * If someone deletes or edits the `@source` line below, this test fails
 * before the visual regression reaches a human.
 */
describe("index.css @source directive", () => {
  const indexCss = readFileSync(
    resolve(__dirname, "..", "index.css"),
    "utf8",
  );

  it("keeps a @source pointing at catalyst-ui so its utility classes bundle", () => {
    // Match either the yarn-linked workspace path OR the plain node_modules
    // path (whichever the current install is using). Both must land inside
    // the catalyst-ui package.
    expect(indexCss).toMatch(/@source\s+["'][^"']*catalyst-ui[^"']*["']/);
  });

  it("preserves the @theme inline block so tokens map to Tailwind utilities", () => {
    expect(indexCss).toMatch(/@theme\s+inline\s*\{/);
    expect(indexCss).toMatch(/--color-primary:\s*var\(--primary\)/);
    expect(indexCss).toMatch(/--color-background:\s*var\(--background\)/);
    expect(indexCss).toMatch(/--color-popover:\s*var\(--popover\)/);
  });
});
