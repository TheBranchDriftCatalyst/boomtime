import { describe, expect, it } from "vitest";
import {
  MAX_CHART_POINTS,
  bucketAvg,
  bucketDates,
  bucketGroups,
  bucketSum,
} from "@/viz/bucket";

describe("bucketGroups", () => {
  it("is the identity when n <= MAX_CHART_POINTS (62)", () => {
    expect(bucketGroups(3)).toEqual([[0], [1], [2]]);
    const g62 = bucketGroups(62);
    expect(g62).toHaveLength(62);
    expect(g62[0]).toEqual([0]);
    expect(g62[61]).toEqual([61]);
  });

  it("groups by size=ceil(n/62) when n > 62", () => {
    // 63 -> ceil(63/62)=2 => 32 groups (31 pairs + 1 last)
    const g = bucketGroups(63);
    const size = Math.ceil(63 / MAX_CHART_POINTS);
    expect(size).toBe(2);
    expect(g[0]).toEqual([0, 1]);
    // last group has the remainder
    expect(g[g.length - 1]).toEqual([62]);
    // full coverage of every index, no gaps/dupes
    expect(g.flat()).toEqual([...Array(63).keys()]);
  });

  it("covers all indices for large n (1000) with last min(size,n-i)", () => {
    const n = 1000;
    const g = bucketGroups(n);
    const size = Math.ceil(n / MAX_CHART_POINTS);
    expect(g[0]).toHaveLength(size);
    expect(g.flat()).toEqual([...Array(n).keys()]);
    // last bucket is a remainder <= size
    expect(g[g.length - 1].length).toBeLessThanOrEqual(size);
  });

  it("n=1 -> single group", () => {
    expect(bucketGroups(1)).toEqual([[0]]);
  });
});

describe("bucket aggregators", () => {
  const groups = [
    [0, 1],
    [2, 3],
  ];

  it("bucketSum sums with ??0 for holes", () => {
    expect(bucketSum(groups, [1, 2, 3])).toEqual([3, 3]); // [1+2, 3+0]
  });

  it("bucketAvg averages over the bucket length", () => {
    expect(bucketAvg(groups, [2, 4, 10, 0])).toEqual([3, 5]);
  });

  it("bucketDates picks the first index of each group", () => {
    const dates = ["a", "b", "c", "d"];
    expect(bucketDates(groups, dates)).toEqual(["a", "c"]);
  });
});
