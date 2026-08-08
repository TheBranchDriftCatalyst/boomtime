// GithubStatTiles.tsx (gaka-csx P3) — the FIRST GitHub-only surface on the
// Overview. It is a GH-ONLY tile strip, so it obeys invariant case (B):
//
//   • Feature entirely off (github_connect_enabled === false) → render NOTHING.
//   • Feature on but not linked / no data (404 or an empty cache) → a single
//     friendly "Connect GitHub" CTA card that links to the connect flow in
//     Settings › Profile.
//   • Feature on + data present → a row of GitHub-branded stat tiles.
//
// The stats come from a SEPARATE optional query (qk.githubStats / api
// .getGithubStats, from P2). It is fully isolated here: its error/empty states
// resolve to the CTA in-component and NEVER bubble to (or block) the Overview.

import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import {
  Award,
  Flame,
  Github,
  GitCommitHorizontal,
  GitPullRequest,
} from "lucide-react";
import { StatCard } from "@thebranchdriftcatalyst/catalyst-ui/components/StatCard";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Card, CardContent } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { usePublicConfig } from "@/lib/usePublicConfig";
import { currentGithubStreak } from "@/features/overview/githubStreak";
import type { GithubStatsPayload } from "@/types/github";

// GitHub-brand green — the shared accent for every GH surface, deliberately
// distinct from --primary so GitHub data always reads as its own series.
const GH_ACCENT = "#39d353";

const SETTINGS_PROFILE = "/app/settings?tab=profile";

// hasGithubData decides whether a (successfully fetched) payload actually holds
// something worth tiling. A connected-but-never-synced or brand-new account
// yields an all-zero payload → treat as no-data → CTA.
function hasGithubData(data: GithubStatsPayload | undefined): boolean {
  if (!data) return false;
  const t = data.totals;
  return (
    t.totalContributions > 0 ||
    t.commits > 0 ||
    t.pullRequestReviews > 0 ||
    (data.contributionGrid?.some((d) => d.count > 0) ?? false)
  );
}

/** The "Connect GitHub" empty-state — invariant case (B)'s friendly CTA. */
function ConnectGithubCta() {
  return (
    <Card
      className="border-dashed"
      style={{ borderColor: `${GH_ACCENT}66` }}
    >
      <CardContent className="flex flex-col items-center gap-3 py-8 text-center">
        <span
          className="flex h-11 w-11 items-center justify-center rounded-full"
          style={{ backgroundColor: `${GH_ACCENT}1f`, color: GH_ACCENT }}
        >
          <Github className="h-5 w-5" />
        </span>
        <div>
          <h3 className="text-sm font-semibold">Connect GitHub</h3>
          <p className="mt-1 max-w-sm text-sm text-muted-foreground">
            Connect GitHub to see your commits &amp; reviews alongside your
            coding time.
          </p>
        </div>
        <Button asChild size="sm">
          <Link to={SETTINGS_PROFILE}>
            <Github className="mr-2 h-4 w-4" />
            Connect GitHub
          </Link>
        </Button>
      </CardContent>
    </Card>
  );
}

/**
 * GithubStatTiles — the Overview's GitHub stat strip. Mount it anywhere in the
 * dashboard; it self-fetches and self-hides per the rules above.
 */
export function GithubStatTiles() {
  const { config } = usePublicConfig();
  const enabled = config.github_connect_enabled;

  const q = useQuery({
    queryKey: qk.githubStats(),
    queryFn: () => api.getGithubStats(),
    enabled, // never fires when the feature is off
    staleTime: 60_000,
    retry: false, // a 404 (not connected) resolves straight to the CTA
  });

  // Feature entirely off → render NOTHING (invariant B).
  if (!enabled) return null;

  // Still resolving — stay quiet rather than flash the CTA before data lands.
  if (q.isLoading) return null;

  // Not connected (query errored / 404) OR connected-but-empty → the CTA.
  if (q.isError || !hasGithubData(q.data)) {
    return <ConnectGithubCta />;
  }

  const data = q.data!;

  return (
    <section className="space-y-3">
      <div className="flex items-center gap-2">
        <Github className="h-4 w-4" style={{ color: GH_ACCENT }} />
        <h2 className="text-sm font-semibold">
          GitHub activity
          {data.login ? (
            <span className="ml-1 font-normal text-muted-foreground">
              @{data.login}
            </span>
          ) : null}
        </h2>
      </div>
      <GithubTilesBody data={data} />
    </section>
  );
}

/**
 * GithubTilesBody (gaka-2ud P5) — the PURE, presentational 4-tile strip built
 * from an already-fetched GithubStatsPayload. Extracted from GithubStatTiles so
 * the SAME tiles render on the authed Overview (self-fetch wrapper above) and on
 * the PUBLIC profile (publicprofile/GithubCard, which fetches the public mirror
 * by slug). One implementation → the two surfaces can never drift. Takes no
 * accent: the StatCards use semantic `accent` variants that read on any theme.
 */
export function GithubTilesBody({ data }: { data: GithubStatsPayload }) {
  const t = data.totals;
  const streak = currentGithubStreak(data.contributionGrid ?? []);
  return (
    // GH-green glow rail marks the whole strip as the GitHub series.
    <div
      className="grid grid-cols-1 gap-4 rounded-lg sm:grid-cols-2 xl:grid-cols-4"
      style={{ boxShadow: `inset 3px 0 0 0 ${GH_ACCENT}` }}
    >
      <StatCard
        name="Total commits"
        value={t.commits.toLocaleString()}
        icon={<GitCommitHorizontal className="h-6 w-6" />}
        accent="success"
      />
      <StatCard
        name="PR reviews"
        value={t.pullRequestReviews.toLocaleString()}
        icon={<GitPullRequest className="h-6 w-6" />}
        accent="info"
      />
      <StatCard
        name="Current GitHub streak"
        value={streak === 1 ? "1 day" : `${streak} days`}
        icon={<Flame className="h-6 w-6" />}
        accent="warning"
      />
      <StatCard
        name="Total contributions"
        value={t.totalContributions.toLocaleString()}
        icon={<Award className="h-6 w-6" />}
        accent="success"
      />
    </div>
  );
}
