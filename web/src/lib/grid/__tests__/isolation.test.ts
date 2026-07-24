// isolation.test.ts — enforces the extraction contract from gaka-6qg /
// design-brief: web/src/lib/grid/* must NOT import from boomtime-domain
// modules (@/features, @/components, @/theme, @/lib/api,
// @thebranchdriftcatalyst/catalyst-ui). Allowed: react, react-dom,
// react-grid-layout, react-resizable, lucide-react, and relative imports
// inside the grid folder.
//
// When we lift the folder into catalyst-ui, this test SHOULD go with it
// (renamed) so the isolation stays enforced at the future package boundary.
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const GRID_DIR = path.resolve(__dirname, "..");

const FORBIDDEN_PREFIXES = [
  "@/features/",
  "@/components/",
  "@/theme/",
  "@/lib/api",
  "@/lib/queryKeys",
  "@thebranchdriftcatalyst/catalyst-ui",
];

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    if (entry.name === "__tests__") continue;
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) walk(full, out);
    else if (/\.(tsx?|css)$/.test(entry.name)) out.push(full);
  }
  return out;
}

describe("grid primitive isolation", () => {
  const files = walk(GRID_DIR);

  it("contains at least the expected public entrypoints", () => {
    // Sanity: catch a rename that would let this test silently pass on 0 files.
    expect(files.some((f) => f.endsWith("DraggableGridLayout.tsx"))).toBe(true);
    expect(files.some((f) => f.endsWith("WidgetHost.tsx"))).toBe(true);
    expect(files.some((f) => f.endsWith("ChartToggle.tsx"))).toBe(true);
  });

  it.each(FORBIDDEN_PREFIXES)("no file imports from %s", (prefix) => {
    const violators: string[] = [];
    for (const file of files) {
      const src = fs.readFileSync(file, "utf8");
      // Cheap `from "<prefix>...` match — covers both single & double quotes.
      const re = new RegExp(
        `from\\s+['"]${prefix.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}[^'"]*['"]`,
        "g",
      );
      if (re.test(src)) violators.push(path.relative(GRID_DIR, file));
    }
    expect(violators).toEqual([]);
  });
});
