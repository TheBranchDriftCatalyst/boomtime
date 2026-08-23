// useAwards.ts — server-side awards hooks (boom-hc6.4).
//
// Replaces the client-side evaluate() pipeline that used to run in
// LabelsShowcase + HeroIdentity. The server now:
//   1. runs the evaluator on the caller's payload
//   2. writes ledger rows atomically (own path only — public visits don't
//      pollute the ledger)
//   3. returns []LabelAward
//
// This file is the only entry point widgets should use. useLogAwards is
// deleted at the same site — server-authoritative ledger writes make the
// client POST redundant.
//
// Scope detection: the app renders LabelsShowcase both on /app (own view)
// and on /p/:slug (public view). We use react-router's useParams to detect
// the slug — present means public, absent means own. This mirrors the
// existing useAwardStreaks(slug?) shape.

import { useQuery } from "@tanstack/react-query";
import { useParams } from "react-router";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import type { LabelAward } from "./types";

// A single hook that picks the right endpoint based on the current route.
// Widgets don't need to know whether they're in a public view or not —
// route context answers that.
export function useAwards(): LabelAward[] {
  const params = useParams<{ slug?: string }>();
  const slug = params.slug;
  const own = useQuery({
    queryKey: qk.awards("own"),
    queryFn: () => api.getOwnAwards(),
    // 30s: matches the server's Cache-Control: private, max-age=30. FE
    // refetches on the next mount / staleness — cheap since the evaluate
    // runs against a 60d payload that changes slowly.
    staleTime: 30_000,
    enabled: !slug,
    // A public visitor hitting /p/:slug in a browser with no session
    // shouldn't 401-loop when this hook fires — retry:false keeps it quiet.
    retry: false,
  });
  const pub = useQuery({
    queryKey: qk.awards("public", slug),
    queryFn: () => (slug ? api.getPublicAwards(slug) : Promise.resolve([])),
    // 180s: matches the server's Cache-Control: public, max-age=180. Public
    // profiles get visitor traffic; longer stale keeps the cache hot.
    staleTime: 180_000,
    enabled: !!slug,
    retry: false,
  });
  const list = slug ? pub.data : own.data;
  // Server returns condition: unknown; cast to the LabelAward shape here so
  // consumers keep the same type. If the server ever ships a shape change
  // this cast is the one central place to update.
  return (list as LabelAward[] | undefined) ?? [];
}

// Overload for callers that already know they want the caller's own set
// (e.g. an admin-only page). Skips the route sniff.
export function useOwnAwards(): LabelAward[] {
  const q = useQuery({
    queryKey: qk.awards("own"),
    queryFn: () => api.getOwnAwards(),
    staleTime: 30_000,
    retry: false,
  });
  return (q.data as LabelAward[] | undefined) ?? [];
}

// Overload for known-slug callers.
export function usePublicAwards(slug: string): LabelAward[] {
  const q = useQuery({
    queryKey: qk.awards("public", slug),
    queryFn: () => api.getPublicAwards(slug),
    staleTime: 180_000,
    enabled: !!slug,
    retry: false,
  });
  return (q.data as LabelAward[] | undefined) ?? [];
}
