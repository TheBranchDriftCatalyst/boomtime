// booksExplore unit tests — lock the pure model against the backend registry
// and the DSL contract. These are the load-bearing invariants the component
// leans on:
//
//   - The runtime measure MUST NOT offer "author" (internal/query/domains.go
//     omits it; asking would 400). If someone widens MEASURES.runtime.dims to
//     include author, dimAllowed flips and this fails.
//   - The grouped spec buckets top-N with an Other roll-up and asks for
//     TOP_N + 1 rows so the trailing Other row is never clipped by `limit`.
//   - Value formatting: books → integer count; runtime → h/m minutes.
import { describe, expect, it } from "vitest";
import {
  OTHER_KEY,
  TOP_N,
  buildBooksGroupSpec,
  dimAllowed,
  formatMeasureValue,
  formatMinutes,
  groupKeyLabel,
  isOtherRow,
} from "./booksExplore";

describe("booksExplore model", () => {
  it("mirrors the backend dim whitelist per measure", () => {
    // books: every reading dim is groupable.
    for (const d of ["source", "status", "series", "author", "genre"] as const) {
      expect(dimAllowed(d, "books")).toBe(true);
    }
    // runtime: same set MINUS author (not on the runtime measure's registry).
    expect(dimAllowed("author", "runtime")).toBe(false);
    for (const d of ["source", "status", "series", "genre"] as const) {
      expect(dimAllowed(d, "runtime")).toBe(true);
    }
  });

  it("builds a top-N + Other bucketed, value-desc spec with room for Other", () => {
    const spec = buildBooksGroupSpec("author", "books");
    expect(spec).toMatchObject({
      domain: "reading",
      measure: "books",
      group: "author",
      bucket: { topN: TOP_N, other: true },
      sort: { field: "value", desc: true },
    });
    // limit must leave room for the appended Other row.
    expect(spec.limit).toBe(TOP_N + 1);
  });

  it("formats measure values by kind", () => {
    expect(formatMeasureValue(1234, "books")).toBe("1,234");
    expect(formatMeasureValue(90, "runtime")).toBe("1h 30m");
    expect(formatMinutes(45)).toBe("45m");
    expect(formatMinutes(120)).toBe("2h");
    expect(formatMinutes(0)).toBe("0m");
  });

  it("labels null-dimension keys as (none) and detects the Other row", () => {
    expect(groupKeyLabel("")).toBe("(none)");
    expect(groupKeyLabel("Sanderson")).toBe("Sanderson");
    expect(isOtherRow(OTHER_KEY)).toBe(true);
    expect(isOtherRow("Fiction")).toBe(false);
  });
});
