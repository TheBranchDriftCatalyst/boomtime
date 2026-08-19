import type { CurationRule } from "@/types/api";

/** Group curation rules by axis, preserving encounter order. */
export function groupByAxis(rules: CurationRule[]): Map<string, CurationRule[]> {
  const map = new Map<string, CurationRule[]>();
  for (const r of rules) {
    const arr = map.get(r.axis) ?? [];
    arr.push(r);
    map.set(r.axis, arr);
  }
  return map;
}
