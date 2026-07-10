import { describe, expect, it } from "vitest";
import {
  COMBOBOX_MAX_RESULTS,
  canCreate,
  exactExists,
  filterOptions,
} from "@/lib/comboboxFilter";

const opts = (...vals: string[]) => vals.map((value) => ({ value }));

describe("filterOptions", () => {
  it("case-insensitive substring match", () => {
    const o = opts("TypeScript", "JavaScript", "Go");
    expect(filterOptions(o, "script").map((x) => x.value)).toEqual([
      "TypeScript",
      "JavaScript",
    ]);
    expect(filterOptions(o, "GO").map((x) => x.value)).toEqual(["Go"]);
  });

  it("empty search returns all (trimmed)", () => {
    const o = opts("a", "b");
    expect(filterOptions(o, "   ")).toHaveLength(2);
  });

  it("caps at COMBOBOX_MAX_RESULTS (200)", () => {
    const many = opts(...Array.from({ length: 500 }, (_, i) => `item${i}`));
    expect(filterOptions(many, "")).toHaveLength(COMBOBOX_MAX_RESULTS);
  });
});

describe("exactExists", () => {
  it("is case-insensitive and trims", () => {
    const o = opts("Meeting");
    expect(exactExists(o, "  meeting ")).toBe(true);
    expect(exactExists(o, "meet")).toBe(false);
  });
});

describe("canCreate", () => {
  const o = opts("Meeting");
  it("true only when creatable + non-empty + not an exact match", () => {
    expect(canCreate(o, "Standup", true)).toBe(true);
  });
  it("false when not creatable", () => {
    expect(canCreate(o, "Standup", false)).toBe(false);
  });
  it("false when search is empty/whitespace", () => {
    expect(canCreate(o, "   ", true)).toBe(false);
  });
  it("false when an exact (case-insensitive) match exists", () => {
    expect(canCreate(o, "meeting", true)).toBe(false);
  });
});
