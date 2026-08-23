// catalogEntries.test.ts — drift guards for the flattened catalog list.
// Cheap, high-signal: catches a new WIDGET_CATALOG kind landing without a
// CATEGORY_BY_KIND entry (silently bucketed into "Other") and keeps
// `embeddable` honest against the specs.json `target`.
import { describe, expect, it } from "vitest";
import type { PublicConfig } from "@shared/types/meta";
import { WIDGET_CATALOG } from "@shared/features/widgets/catalog";
import {
  CATALOG_CATEGORIES,
  CATALOG_WIDGETS,
  visibleCatalogWidgets,
} from "./catalogEntries";

const READING_KINDS = [
  "reading-listening-trend",
  "reading-books-by-genre",
  "reading-top-series",
  "reading-finished-per-month",
  "reading-listening-in-range",
];

function config(over: Partial<PublicConfig>): PublicConfig {
  return {
    registration_enabled: true,
    auth_provider: "local",
    oidc_enabled: false,
    billing_enabled: false,
    beta_flags: {},
    github_connect_enabled: false,
    books_enabled: false,
    ...over,
  };
}

describe("CATALOG_WIDGETS", () => {
  it("has exactly one entry per WIDGET_CATALOG kind, same order", () => {
    expect(CATALOG_WIDGETS.map((e) => e.kind)).toEqual(WIDGET_CATALOG.map((e) => e.kind));
  });

  it("every kind has a REAL category — none falls through to the Other catch-all", () => {
    const other = CATALOG_WIDGETS.filter((e) => e.category === "Other");
    expect(other.map((e) => e.kind)).toEqual([]);
  });

  it("every entry's category is one CATALOG_CATEGORIES lists", () => {
    for (const entry of CATALOG_WIDGETS) {
      expect(CATALOG_CATEGORIES, entry.kind).toContain(entry.category);
    }
  });

  it("embeddable is true iff target is both", () => {
    for (const entry of CATALOG_WIDGETS) {
      expect(entry.embeddable, entry.kind).toBe(entry.target === "both");
    }
  });

  it("title/description are non-empty for every entry", () => {
    for (const entry of CATALOG_WIDGETS) {
      expect(entry.title.length, entry.kind).toBeGreaterThan(0);
      expect(entry.description.length, entry.kind).toBeGreaterThan(0);
    }
  });
});

// boom-qcxg — reading-domain registration + feature gate.
describe("reading-domain catalog widgets", () => {
  const byKind = new Map(CATALOG_WIDGETS.map((e) => [e.kind, e]));

  it("registers every reading kind under the Reading category", () => {
    expect(CATALOG_CATEGORIES).toContain("Reading");
    for (const kind of READING_KINDS) {
      const entry = byKind.get(kind);
      expect(entry, `${kind} missing from the catalog`).toBeDefined();
      expect(entry!.category, kind).toBe("Reading");
    }
  });

  it("marks every reading kind fe-only (not embeddable) and books_enabled-gated", () => {
    for (const kind of READING_KINDS) {
      const entry = byKind.get(kind)!;
      expect(entry.target, kind).toBe("fe-only");
      expect(entry.embeddable, kind).toBe(false);
      expect(entry.flag, kind).toBe("books_enabled");
    }
  });

  it("Reading is the ONLY flag-gated category (no other kind ships a flag by accident)", () => {
    const flagged = CATALOG_WIDGETS.filter((e) => e.flag).map((e) => e.kind).sort();
    expect(flagged).toEqual([...READING_KINDS].sort());
  });
});

describe("visibleCatalogWidgets — the books_enabled gate", () => {
  it("HIDES every reading kind when books_enabled is off", () => {
    const kinds = visibleCatalogWidgets(config({ books_enabled: false })).map((e) => e.kind);
    for (const r of READING_KINDS) expect(kinds, r).not.toContain(r);
  });

  it("LISTS every reading kind when books_enabled is on", () => {
    const kinds = visibleCatalogWidgets(config({ books_enabled: true })).map((e) => e.kind);
    for (const r of READING_KINDS) expect(kinds, r).toContain(r);
  });

  it("never drops an UNFLAGGED widget regardless of the flag", () => {
    const off = visibleCatalogWidgets(config({ books_enabled: false }));
    const unflagged = CATALOG_WIDGETS.filter((e) => !e.flag);
    // Toggling books_enabled off removes exactly the reading kinds, nothing else.
    expect(off.map((e) => e.kind)).toEqual(unflagged.map((e) => e.kind));
  });
});
