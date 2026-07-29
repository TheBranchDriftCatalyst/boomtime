// useAwardStreaks — client for the label-award streak ledger
// (gaka-mwp-streaks).
//
// Post gaka-hc6.5: the WRITE path is gone. Server /awards writes ledger
// rows on its own read (see internal/handler/awards_eval.go), so no
// client-side POST is needed. The historical backfill tool that used
// to loop day-by-day is now a single POST /awards/backfill call
// (see gaka-hc6.5.1).
//
// This file now only owns the READ path — the streak map that
// LabelChip needs to render Nx badges.

import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";

/**
 * Fetches the current streak map for a user. Pass a public slug to
 * read a public profile's streaks; omit for the caller's own.
 *
 * Returns undefined during the initial fetch. Absent labels have no
 * active streak.
 */
export function useAwardStreaks(slug?: string): Record<string, number> {
  const q = useQuery({
    queryKey: qk.awardStreaks(slug),
    queryFn: () =>
      slug ? api.getPublicAwardStreaks(slug) : api.getAwardStreaks(),
    staleTime: 60_000,
    // On a public profile with no auth session, hitting /own/streaks
    // 401s — that's harmless; empty map is the correct default.
    retry: false,
  });
  return q.data ?? {};
}
