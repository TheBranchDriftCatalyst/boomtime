// GithubCard.tsx (gaka-2ud Phase 5) — the GitHub stats surface for the PUBLIC
// profile (/p/:slug). It fetches the UNAUTH public mirror
// (GET /api/public/profile/:slug/github/stats via api.getPublicGithubStats) and
// renders the SAME shared chart bodies the in-app P3/P4 widgets use
// (GithubTilesBody + GithubCommitsChart + GithubReposChart +
// GithubLanguagesChart) — one implementation of every chart, so the public and
// authed surfaces can never drift.
//
// PUBLIC SEMANTICS (critical difference from the in-app widgets): an external
// viewer can't connect the owner's GitHub, so there is NEVER a "Connect GitHub"
// CTA and NEVER an error state here. The card resolves to exactly two visible
// outcomes:
//
//   • data present  → the charts.
//   • feature off / 404 (not public / no cache) / empty payload / any error
//                    → render NOTHING (the card silently hides).
//   • loading        → a subtle skeleton.
//
// This preserves the public profile for owners without GitHub and for foreign
// viewers alike — the tile simply isn't there.
//
// DOSSIER STYLING: the charts render in the profile's own accent
// (`cssVar("--primary")` — amber/crimson under the arasaka dossier skin) rather
// than the in-app GitHub green, so the GitHub data reads as part of the dossier
// instead of a foreign brand splash. The accent threads through the shared chart
// bodies' `accent` prop; the in-app widgets keep their green default untouched.

import { useQuery } from "@tanstack/react-query";
import { Github } from "lucide-react";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import { usePublicConfig } from "@shared/lib/usePublicConfig";
import { cssVar } from "@shared/viz/d3/useChartFrame";
import { hasGithubData } from "@shared/features/overview/githubStatsWidget";
import { GithubTilesBody } from "@shared/features/overview/GithubStatTiles";
import {
  GithubCommitsChart,
  GithubReposChart,
  GithubLanguagesChart,
} from "@shared/features/overview/GithubCharts";
import type { GithubStatsPayload } from "@shared/types/github";

export interface GithubCardProps {
  /** The public profile slug — drives the unauth public-stats fetch. */
  slug: string;
}

/**
 * GithubCard — public-profile GitHub stats tile. Self-fetches by slug and
 * self-hides on anything short of real data (see the module doc for the
 * hide-on-empty invariant). Safe to mount on any /p/:slug layout.
 */
export function GithubCard({ slug }: GithubCardProps) {
  const { config } = usePublicConfig();
  // Gate on the instance feature flag (parity with the in-app widgets) so a
  // GitHub-disabled deployment never fires the request at all. When off the
  // card just isn't there.
  const enabled = config.github_connect_enabled;

  const query = useQuery({
    queryKey: qk.publicGithubStats(slug),
    queryFn: () => api.getPublicGithubStats(slug),
    enabled: enabled && !!slug,
    staleTime: 60_000,
    // A 404 (not public / no cache) resolves straight to "render nothing" — no
    // retry, no error surface.
    retry: false,
  });

  if (!enabled) return null;
  // Loading → a subtle skeleton rather than flashing empty space then content.
  if (query.isLoading) return <GithubCardSkeleton />;
  // Not public / no cache / errored / connected-but-empty → render NOTHING.
  // NO CTA and NO error on the public page (the additive public invariant).
  if (query.isError || !hasGithubData(query.data)) return null;

  return <GithubCardBody data={query.data!} />;
}

function GithubCardBody({ data }: { data: GithubStatsPayload }) {
  // Match the dossier accent instead of the in-app GitHub green so the GitHub
  // data reads as part of the profile. cssVar resolves `--primary` to the
  // active theme's concrete color (needed because the D3 commits area sets SVG
  // attributes, which don't resolve `var(--…)`).
  const accent = cssVar("--primary");
  return (
    <div className="flex h-full min-h-0 w-full flex-col gap-4 overflow-y-auto p-1">
      {/* Dossier-styled attribution line — restrained, mono, uppercase. */}
      <div
        className="flex items-center gap-2 font-mono text-[10px] uppercase tracking-[0.18em] text-[color:var(--muted-foreground)]"
        data-testid="github-card-header"
      >
        <Github className="h-3.5 w-3.5" style={{ color: accent }} aria-hidden />
        <span>GitHub Activity</span>
        {data.login ? (
          <span style={{ color: accent }}>· @{data.login}</span>
        ) : null}
      </div>

      {/* Key tiles (shared P3 body): commits · PR reviews · streak · total. */}
      <GithubTilesBody data={data} />

      {/* Contribution grid summary → weekly commits area (shared P4 body). */}
      <div className="min-h-[170px]">
        <GithubCommitsChart data={data} accent={accent} />
      </div>

      {/* Top repositories + language breakdown, side by side on wide tiles. */}
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
        <div className="min-h-[200px]">
          <GithubReposChart data={data} accent={accent} />
        </div>
        <div className="min-h-[200px]">
          <GithubLanguagesChart data={data} accent={accent} />
        </div>
      </div>
    </div>
  );
}

/** A quiet, accent-tinted skeleton while the public stats resolve. */
function GithubCardSkeleton() {
  return (
    <div
      className="flex h-full min-h-0 w-full flex-col gap-3 p-1"
      data-testid="github-card-skeleton"
      aria-hidden
    >
      <div
        className="h-3 w-1/3 rounded-sm"
        style={{ background: "color-mix(in oklab, var(--primary) 12%, transparent)" }}
      />
      <div
        className="h-16 w-full rounded-md"
        style={{ background: "color-mix(in oklab, var(--primary) 8%, transparent)" }}
      />
      <div
        className="min-h-[120px] flex-1 rounded-md"
        style={{ background: "color-mix(in oklab, var(--primary) 6%, transparent)" }}
      />
    </div>
  );
}
