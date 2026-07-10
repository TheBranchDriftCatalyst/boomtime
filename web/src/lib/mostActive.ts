// Pure helpers for the "most active X" stat cards, extracted from the inline
// logic that was duplicated across Overview and Projects so it can be unit
// tested and shared. Excludes both the literal "Other" catch-all (no-language
// browsing/meeting heartbeats) and the aggregated "Other (N more)" bucket.

interface NamedTotal {
  name: string;
  totalSeconds: number;
}

/** True for the aggregate buckets that should never win a "most active" pick. */
export function isOtherName(name: string): boolean {
  return name === "Other" || name.startsWith("Other (");
}

/**
 * The name of the top resource by tracked time, excluding the "Other" buckets.
 * Returns "-" when there is nothing left to pick.
 */
export function mostActive(items: NamedTotal[]): string {
  return (
    [...items]
      .filter((r) => !isOtherName(r.name))
      .sort((a, b) => b.totalSeconds - a.totalSeconds)[0]?.name ?? "-"
  );
}
