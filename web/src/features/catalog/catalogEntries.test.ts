// catalogEntries.test.ts — drift guards for the flattened catalog list.
// Cheap, high-signal: catches a new WIDGET_CATALOG kind landing without a
// CATEGORY_BY_KIND entry (silently bucketed into "Other") and keeps
// `embeddable` honest against the specs.json `target`.
import { describe, expect, it } from "vitest";
import { WIDGET_CATALOG } from "@/features/widgets/catalog";
import { CATALOG_CATEGORIES, CATALOG_WIDGETS } from "./catalogEntries";

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
