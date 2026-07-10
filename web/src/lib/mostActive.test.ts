import { describe, expect, it } from "vitest";
import { isOtherName, mostActive } from "@/lib/mostActive";

describe("isOtherName", () => {
  it("matches the literal 'Other' and 'Other (N more)' bucket", () => {
    expect(isOtherName("Other")).toBe(true);
    expect(isOtherName("Other (7 more)")).toBe(true);
  });
  it("does not match real names", () => {
    expect(isOtherName("Otherworld")).toBe(false);
    expect(isOtherName("TypeScript")).toBe(false);
  });
});

describe("mostActive", () => {
  it("returns the top by totalSeconds excluding Other buckets", () => {
    const items = [
      { name: "Other", totalSeconds: 9999 },
      { name: "Other (3 more)", totalSeconds: 8888 },
      { name: "gakatime", totalSeconds: 400 },
      { name: "docs", totalSeconds: 100 },
    ];
    expect(mostActive(items)).toBe("gakatime");
  });

  it("returns '-' when nothing is left after excluding Other", () => {
    expect(mostActive([{ name: "Other", totalSeconds: 5 }])).toBe("-");
    expect(mostActive([])).toBe("-");
  });

  it("does not mutate the input array", () => {
    const items = [
      { name: "a", totalSeconds: 1 },
      { name: "b", totalSeconds: 2 },
    ];
    const copy = [...items];
    mostActive(items);
    expect(items).toEqual(copy);
  });
});
