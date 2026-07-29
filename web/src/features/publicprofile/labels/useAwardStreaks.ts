// useAwardStreaks — client for the label-award streak ledger
// (gaka-mwp-streaks).
//
// Two responsibilities in one file so consumers only import once:
//
//   1. `useLogAwards(awards, catalog)` — fire-and-forget POST that
//      tells the server "these labels just fired for the current user".
//      Server upserts into award_ledger keyed by (user, label,
//      period_start-in-user-tz). Idempotent within a period.
//
//      Only fires on OWN-profile views (auth cookie present + no public
//      slug context). Public viewers see badges via `useAwardStreaks`
//      but don't write to another user's ledger.
//
//   2. `useAwardStreaks(slug?)` — fetches the current streak map
//      `{[labelId]: number}` for the target user (own if `slug` is
//      undefined, public-scoped if a slug is passed). Cached 60s via
//      react-query so multiple LabelChip mounts share one fetch.
//
// The label→period mapping mirrors the server-side
// db.KindDefaultPeriod + labels.period_default override. Kept in
// sync manually — a drift here means labels won't be logged with
// their intended cadence.

import { useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import type { LabelAward, LabelSpec } from "./types";

/** Period cadence a label recurs on. Must match db.PeriodType. */
export type LabelPeriod = "daily" | "weekly" | "monthly" | "lifetime";

/** Fallback period per label kind — mirrors internal/db/award_ledger.go
 *  KindDefaultPeriod. Lifetime labels aren't ledger-eligible (no streaks). */
const KIND_DEFAULT_PERIOD: Record<LabelSpec["kind"], LabelPeriod> = {
  tier: "lifetime",
  tribe: "lifetime",
  archetype: "weekly",
  meme: "weekly",
  patch: "daily",
};

/**
 * resolvePeriod returns the effective cadence for a spec — per-label
 * `periodDefault` override wins over the kind-based default.
 */
export function resolvePeriod(spec: {
  kind: LabelSpec["kind"];
  periodDefault?: string;
}): LabelPeriod {
  const p = (spec.periodDefault || "").trim();
  if (p === "daily" || p === "weekly" || p === "monthly" || p === "lifetime") {
    return p;
  }
  return KIND_DEFAULT_PERIOD[spec.kind] ?? "weekly";
}

// ---------- WRITE PATH: useLogAwards ----------------------------------

interface LogAwardsOpts {
  /** Set to false to hard-disable (e.g. rendering a public profile
   *  offline). Even when true, the hook internally gates on
   *  "am I authenticated" so anonymous viewers never POST. */
  enabled?: boolean;
}

/**
 * Fires an idempotent POST to /awards/log with every non-lifetime
 * award that just evaluated for the CURRENT authenticated user.
 * No-op when:
 *   - the viewer isn't logged in (POST would 401 anyway)
 *   - opts.enabled === false
 *   - no non-lifetime awards fired
 *
 * Callers that render another user's public profile MUST NOT call
 * this hook (or must pass enabled:false) — the POST always writes
 * to the authenticated user's ledger, so mis-attribution is the
 * failure mode we're guarding against.
 *
 * Deduped per-session via a ref so a remount doesn't hammer the
 * endpoint — the actual streak state refreshes lazily via
 * useAwardStreaks's 60s cache.
 */
export function useLogAwards(
  awards: LabelAward[],
  catalog: LabelSpec[],
  opts: LogAwardsOpts = {},
): void {
  // Gate on "is a session cookie present" via the current-user probe.
  // Uses the same react-query key useIsAdmin/Settings/Avatar share, so
  // there's ONE HTTP call across all consumers. In test envs where no
  // fetch is mocked + retry:false + staleTime:Infinity, data stays
  // undefined and logging is naturally disabled.
  const currentUser = useQuery({
    queryKey: ["auth", "current-user"],
    queryFn: () => api.currentUser(),
    staleTime: 60_000,
    retry: false,
  });
  const isLoggedIn = !!currentUser.data;
  const enabled = (opts.enabled ?? true) && isLoggedIn;
  const seen = useRef<string>("");
  useEffect(() => {
    if (!enabled) return;
    if (!awards.length || !catalog.length) return;
    const specById = new Map(catalog.map((s) => [s.id, s]));
    // Narrow to the API's accepted periodTypes (server drops lifetime
    // silently anyway, but the wire type is precise).
    const items: { labelId: string; periodType: "daily" | "weekly" | "monthly" }[] = [];
    for (const a of awards) {
      const spec = specById.get(a.id);
      if (!spec) continue;
      const p = resolvePeriod(spec);
      if (p === "lifetime") continue;
      items.push({ labelId: a.id, periodType: p });
    }
    if (items.length === 0) return;
    // Dedupe: the same evaluate() input produces the same items list.
    // Only fire when the set changes — cheap key that ignores order.
    const key = items
      .map((i) => `${i.labelId}:${i.periodType}`)
      .sort()
      .join(",");
    if (key === seen.current) return;
    seen.current = key;
    // Fire and forget — no need to await, no need to surface errors
    // (server is idempotent + a lost POST just means the streak
    // increments on the next visit).
    void api.logAwards(items).catch(() => {});
  }, [awards, catalog, enabled]);
}

// ---------- READ PATH: useAwardStreaks -------------------------------

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
