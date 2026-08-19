// storage tests — localStorage + memory adapters. Verifies the round-trip
// contract the primitive relies on and confirms bad/absent data returns
// null (so the primitive falls back to defaults).
import { beforeEach, describe, expect, it } from "vitest";
import { localStorageAdapter, memoryAdapter } from "../storage";

describe("localStorageAdapter", () => {
  beforeEach(() => window.localStorage.clear());

  it("save + load round-trip", async () => {
    const a = localStorageAdapter("test-scope");
    const items = [{ i: "x", x: 0, y: 0, w: 6, h: 3, view: "bar" }];
    await a.save(items);
    const loaded = await a.load();
    expect(loaded).toEqual(items);
  });

  it("load returns null when key is absent", async () => {
    const a = localStorageAdapter("empty");
    expect(await a.load()).toBeNull();
  });

  it("load returns null on malformed JSON (not throw)", async () => {
    window.localStorage.setItem("layout:borked", "not-json-at-all");
    const a = localStorageAdapter("borked");
    expect(await a.load()).toBeNull();
  });

  it("keys are namespaced by dashboard id (no cross-contamination)", async () => {
    const a = localStorageAdapter("scope-a");
    const b = localStorageAdapter("scope-b");
    await a.save([{ i: "one", x: 0, y: 0, w: 1, h: 1 }]);
    await b.save([{ i: "two", x: 0, y: 0, w: 1, h: 1 }]);
    expect(await a.load()).toEqual([{ i: "one", x: 0, y: 0, w: 1, h: 1 }]);
    expect(await b.load()).toEqual([{ i: "two", x: 0, y: 0, w: 1, h: 1 }]);
  });
});

describe("memoryAdapter", () => {
  it("preserves initial state on load; save overwrites", async () => {
    const a = memoryAdapter([{ i: "seed", x: 0, y: 0, w: 6, h: 3 }]);
    expect(await a.load()).toEqual([{ i: "seed", x: 0, y: 0, w: 6, h: 3 }]);
    await a.save([{ i: "new", x: 1, y: 1, w: 3, h: 2 }]);
    expect(await a.load()).toEqual([{ i: "new", x: 1, y: 1, w: 3, h: 2 }]);
  });
});
