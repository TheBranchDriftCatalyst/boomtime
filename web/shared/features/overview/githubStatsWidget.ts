// githubStatsWidget.ts (boom-v1k Phase 4) — shared, JSX-free plumbing for the
// GitHub-only chart widgets (commits / repos / languages). Split out of the
// .tsx so those files export ONLY components (react-refresh clean) and so all
// three GH charts read from ONE react-query cache entry.
//
// Every GH surface obeys the additive invariant (see `bd memories
// github-stats`): the feature being off, or the user not being linked / having
// no data, must NEVER surface an error or a broken chart — it renders nothing
// or a friendly Connect-GitHub CTA. This module centralizes the enabled flag,
// the shared query, and the "is there anything worth charting?" predicate.

import { useQuery } from "@tanstack/react-query";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import { usePublicConfig } from "@shared/lib/usePublicConfig";
import type {
  GithubContributionDay,
  GithubStatsPayload,
} from "@shared/types/github";

// GitHub-brand green — the shared accent for every GH surface, matching the P3
// GithubStatTiles so all GitHub data reads as one series, distinct from
// --primary.
export const GH_ACCENT = "#39d353";

// Connect flow lives in Settings › Profile (same target the P3 CTA uses).
export const SETTINGS_PROFILE = "/app/settings?tab=profile";

/**
 * useGithubStatsWidget — the ONE optional GH-stats query, shared by all three
 * chart widgets. react-query dedupes on the identical `qk.githubStats()` key,
 * so mounting three widgets (or the legacy grouping) fires a single request.
 * Mirrors the P3 GithubStatTiles query options exactly: disabled when the
 * feature is off, no retry (a 404 = not-connected resolves straight to the CTA).
 */
export function useGithubStatsWidget() {
  const { config } = usePublicConfig();
  const enabled = config.github_connect_enabled;
  const query = useQuery({
    queryKey: qk.githubStats(),
    queryFn: () => api.getGithubStats(),
    enabled, // never fires when the feature is off
    staleTime: 60_000,
    retry: false,
  });
  return { enabled, query };
}

/**
 * hasGithubData decides whether a successfully-fetched payload holds anything
 * worth charting. A connected-but-never-synced or brand-new account yields an
 * all-zero payload → treat as no-data → CTA. Mirrors GithubStatTiles' predicate
 * so the whole GitHub surface flips between CTA and content as a unit.
 */
export function hasGithubData(data: GithubStatsPayload | undefined): boolean {
  if (!data) return false;
  const t = data.totals;
  return (
    t.totalContributions > 0 ||
    t.commits > 0 ||
    t.stars > 0 ||
    (data.topRepos?.length ?? 0) > 0 ||
    (data.languages?.length ?? 0) > 0 ||
    (data.contributionGrid?.some((d) => d.count > 0) ?? false)
  );
}

// One point of the commits-over-time series: a 7-day bucket's summed
// contributions, keyed by the bucket's first day.
export interface CommitsWeekPoint {
  date: string; // YYYY-MM-DD (bucket start)
  count: number;
}

/**
 * toWeeklyCommits collapses the trailing-year daily contribution grid into
 * ~weekly buckets (7 consecutive days summed). A daily area over 365 points is
 * needle-thin and noisy; weekly buckets give a readable trend without a backend
 * change. The grid is already daily + sorted-able; we sort defensively.
 */
export function toWeeklyCommits(
  grid: GithubContributionDay[] | undefined,
): CommitsWeekPoint[] {
  if (!grid || grid.length === 0) return [];
  const sorted = [...grid].sort((a, b) => a.date.localeCompare(b.date));
  const weeks: CommitsWeekPoint[] = [];
  for (let i = 0; i < sorted.length; i += 7) {
    const chunk = sorted.slice(i, i + 7);
    const count = chunk.reduce((s, d) => s + d.count, 0);
    weeks.push({ date: chunk[0].date, count });
  }
  return weeks;
}
