// PublicDashboard — /p/:slug read-only public dashboard (gaka-6jm.1).
//
// Design notes:
//   - NO auth. NO sidebar. NO header chrome. This route lives outside the
//     /app tree so an unauthenticated visitor never gets bounced through
//     the auth guard.
//   - The payload is fetched via api.getPublicDashboard(slug), which hits
//     GET /api/public/profile/:slug. The backend has already applied the
//     widget.Scrub scrubber and omitted the machines segment — so this
//     component is a straight render of whatever it gets.
//   - Charts are the same D3 components used on the authed Overview.
//     Reuse (don't fork) so any viz improvement lands here for free.
//   - 404 handling: on a 404 (either "slug doesn't exist" or "user
//     disabled"), render a friendly "This profile isn't public" state —
//     no signup CTA, no upsell. Public respect for the user's choice.
import { useQuery } from "@tanstack/react-query";
import { useParams } from "react-router";
import { Clock, Code } from "lucide-react";
import { PieChart } from "@/viz/charts/PieChart";
import { Punchcard } from "@/viz/charts/Punchcard";
import { StatCard } from "@/components/StatCard";
import { Spinner } from "@/components/Spinner";
import { api, ApiError } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { secondsToHms } from "@/lib/utils";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/card";

// Formatter for the header date range. Kept inline (not a shared lib
// import) because the public page's date strings come from the payload as
// ISO strings, not the Date-based ranges the app-shell dashboards use.
function fmtRange(startISO: string, endISO: string): string {
  const start = new Date(startISO);
  const end = new Date(endISO);
  const opts: Intl.DateTimeFormatOptions = {
    year: "numeric",
    month: "short",
    day: "numeric",
  };
  return `${start.toLocaleDateString(undefined, opts)} — ${end.toLocaleDateString(undefined, opts)}`;
}

export function PublicDashboard() {
  const { slug = "" } = useParams<{ slug: string }>();

  const { data, isLoading, error } = useQuery({
    queryKey: qk.publicDashboard(slug),
    queryFn: () => api.getPublicDashboard(slug),
    enabled: !!slug,
    retry: (failureCount, err) => {
      // Never retry a 404 — the user has explicitly opted out (or the
      // slug is bogus). Retrying wastes bandwidth and delays the "not
      // public" render.
      if (err instanceof ApiError && err.status === 404) return false;
      return failureCount < 1;
    },
  });

  // 404 → intentionally-terse "not public" state. No signup CTA — respect
  // the owner's opt-out, don't guilt the visitor.
  if (error instanceof ApiError && error.status === 404) {
    return (
      <PublicShell>
        <div className="mx-auto max-w-md py-24 text-center">
          <h1 className="text-2xl font-semibold">This profile isn't public</h1>
          <p className="mt-2 text-muted-foreground">
            The link may be mistyped, or the owner has disabled public
            visibility.
          </p>
        </div>
      </PublicShell>
    );
  }

  if (isLoading || !data) {
    return (
      <PublicShell>
        <div className="flex h-[60vh] items-center justify-center">
          <Spinner />
        </div>
      </PublicShell>
    );
  }

  // Any other error (500/network) — show a mundane message rather than
  // leaking backend detail. The auth'd Overview handles its own errors;
  // this page is a leaf.
  if (error) {
    return (
      <PublicShell>
        <div className="mx-auto max-w-md py-24 text-center">
          <h1 className="text-2xl font-semibold">Something went wrong</h1>
          <p className="mt-2 text-muted-foreground">Please try again later.</p>
        </div>
      </PublicShell>
    );
  }

  return (
    <PublicShell>
      <div className="mx-auto max-w-6xl space-y-6 px-4 py-8">
        {/* Header */}
        <header className="space-y-1">
          <h1 className="text-3xl font-semibold" data-testid="public-username">
            {data.username}
          </h1>
          <p className="text-sm text-muted-foreground">
            Coding time · {fmtRange(data.startDate, data.endDate)}
          </p>
        </header>

        {/* Aggregate coding-time summary */}
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <StatCard
            name="Total time"
            value={secondsToHms(data.totalSeconds)}
            icon={Clock}
            accent="primary"
          />
          <StatCard
            name="Daily average"
            value={secondsToHms(Math.round(data.dailyAvg))}
            icon={Code}
            accent="info"
          />
        </div>

        {/* Top languages / top projects (pie charts; PieChart handles empty). */}
        <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
          <Card>
            <CardHeader className="pb-2">
              <CardTitle className="text-sm font-semibold text-muted-foreground">
                Top languages
              </CardTitle>
            </CardHeader>
            <CardContent>
              <PieChart items={data.languages} height={280} />
            </CardContent>
          </Card>
          {/* Only render the projects card if any project survived the
              scrubber — otherwise the whole segment might be hidden and we
              shouldn't advertise a big empty card. */}
          {data.projects.length > 0 && (
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm font-semibold text-muted-foreground">
                  Top projects
                </CardTitle>
              </CardHeader>
              <CardContent>
                <PieChart items={data.projects} height={280} />
              </CardContent>
            </Card>
          )}
        </div>

        {/* Punchcard */}
        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm font-semibold text-muted-foreground">
              When they code (UTC)
            </CardTitle>
          </CardHeader>
          <CardContent>
            <Punchcard data={data.punchcard} height={260} />
          </CardContent>
        </Card>
      </div>
    </PublicShell>
  );
}

// PublicShell — bare page chrome. No sidebar/header from AppShell, but the
// ThemeProvider (mounted at main.tsx) is still active so the theme is
// applied. Footer carries the attribution + a home link.
function PublicShell({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-screen flex-col bg-background text-foreground">
      <main className="flex-1">{children}</main>
      <footer className="border-t border-border py-4 text-center text-xs text-muted-foreground">
        Powered by{" "}
        <a href="/" className="hover:underline">
          Boomtime
        </a>{" "}
        — self-hosted coding-time tracker
      </footer>
    </div>
  );
}
