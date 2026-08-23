// githubStreak.ts (boom-csx P3) — pure derivation split out of GithubStatTiles
// so the component file only exports a component (react-refresh clean).

import type { GithubContributionDay } from "@shared/types/github";

/**
 * currentGithubStreak derives the CURRENT contribution streak from the trailing-
 * year grid: the number of consecutive days (walking backward from the most
 * recent grid day) with at least one contribution. Today is given a grace day —
 * a still-empty "today" doesn't zero out a streak that ran through yesterday.
 */
export function currentGithubStreak(grid: GithubContributionDay[]): number {
  if (grid.length === 0) return 0;
  const counts = new Map<string, number>();
  for (const g of grid) counts.set(g.date, g.count);
  const lastDate = grid.map((g) => g.date).sort()[grid.length - 1];
  const cursor = new Date(`${lastDate}T00:00:00Z`);
  const key = (d: Date) => d.toISOString().slice(0, 10);
  // Grace: if the most recent day is still empty, start counting from the
  // prior day so an in-progress day never breaks the streak.
  if ((counts.get(key(cursor)) ?? 0) === 0) {
    cursor.setUTCDate(cursor.getUTCDate() - 1);
  }
  let streak = 0;
  while ((counts.get(key(cursor)) ?? 0) > 0) {
    streak += 1;
    cursor.setUTCDate(cursor.getUTCDate() - 1);
  }
  return streak;
}
