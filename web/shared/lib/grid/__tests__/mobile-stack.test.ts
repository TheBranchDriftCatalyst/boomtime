import { describe, expect, it } from "vitest";
import { stackForMobile } from "@shared/lib/grid/DraggableGridLayout";

type Item = {
  i: string;
  x: number;
  y: number;
  w: number;
  h: number;
};

// stackForMobile collapses a multi-column layout into a single-column stack for
// phone breakpoints (gaka-k26n.2): the public /p/:slug dashboard was illegible
// because the 12-col layout never collapsed. These lock the contract the
// read-only wiring depends on.
describe("stackForMobile", () => {
  it("stacks every tile to x=0 at the mobile column width, no overlap", () => {
    // A classic public-profile row: four w=3 stat tiles side-by-side, then a
    // pair of w=6 charts under them.
    const base: Item[] = [
      { i: "a", x: 0, y: 0, w: 3, h: 2 },
      { i: "b", x: 3, y: 0, w: 3, h: 2 },
      { i: "c", x: 6, y: 0, w: 3, h: 2 },
      { i: "d", x: 9, y: 0, w: 3, h: 2 },
      { i: "chart1", x: 0, y: 2, w: 6, h: 3 },
      { i: "chart2", x: 6, y: 2, w: 6, h: 3 },
    ];

    const out = stackForMobile(base, 1);

    // Every tile is full mobile width, pinned to the left gutter.
    expect(out.every((w) => w.x === 0 && w.w === 1)).toBe(true);

    // y re-flows to the running sum of prior heights — nothing overlaps.
    expect(out.map((w) => w.y)).toEqual([0, 2, 4, 6, 8, 11]);

    // The last tile's bottom edge equals the total stacked height.
    const last = out[out.length - 1];
    expect(last.y + last.h).toBe(2 + 2 + 2 + 2 + 3 + 3);
  });

  it("preserves reading order (row-major: y then x), not input order", () => {
    // Deliberately shuffled + a second row that should interleave by y,x.
    const base: Item[] = [
      { i: "second-row-right", x: 6, y: 5, w: 6, h: 2 },
      { i: "first-row-left", x: 0, y: 0, w: 6, h: 2 },
      { i: "second-row-left", x: 0, y: 5, w: 6, h: 2 },
      { i: "first-row-right", x: 6, y: 0, w: 6, h: 2 },
    ];

    const order = stackForMobile(base, 1).map((w) => w.i);

    expect(order).toEqual([
      "first-row-left",
      "first-row-right",
      "second-row-left",
      "second-row-right",
    ]);
  });

  it("does not mutate the input layout", () => {
    const base: Item[] = [{ i: "a", x: 4, y: 4, w: 3, h: 2 }];
    const snapshot = JSON.parse(JSON.stringify(base));
    stackForMobile(base, 1);
    expect(base).toEqual(snapshot);
  });

  it("carries the mobile column count onto every tile", () => {
    const base: Item[] = [
      { i: "a", x: 0, y: 0, w: 3, h: 2 },
      { i: "b", x: 3, y: 0, w: 9, h: 2 },
    ];
    expect(stackForMobile(base, 2).every((w) => w.w === 2)).toBe(true);
  });
});
