import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { QueryGate } from "@/components/QueryGate";
import { PageToolbar } from "@/components/toolbar/PageToolbar";
import { Badge } from "@thebranchdriftcatalyst/catalyst-ui/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { api } from "@/lib/api";
import {
  classifyReleases,
  parseChangelog,
  type ChangelogEntry,
  type ChangelogRelease,
  type ReleaseStatus,
} from "@/lib/changelog";
import { qk } from "@/lib/queryKeys";
import { cn } from "@/lib/utils";

const STATUS_LABELS: Record<ReleaseStatus, string> = {
  current: "Running",
  newer: "Newer than running",
  older: "Older",
  unreleased: "Unreleased",
};

const STATUS_BADGE_VARIANT: Record<
  ReleaseStatus,
  "default" | "secondary" | "outline"
> = {
  current: "default",
  newer: "secondary",
  older: "outline",
  unreleased: "secondary",
};

// A ring-highlight around the release "you're on right now" so it pops in a
// long changelog. Non-current cards use a plain border.
const CARD_HIGHLIGHT: Record<ReleaseStatus, string> = {
  current: "ring-2 ring-primary/60",
  newer: "ring-1 ring-sky-500/40",
  older: "",
  unreleased: "border-dashed",
};

/**
 * Changelog page: renders the git-cliff-generated CHANGELOG.md as grouped
 * cards per release, highlighting the version the server reports it's running
 * so users can tell at a glance what they have vs. what's landed since.
 *
 * Both the version and changelog endpoints are unauthenticated; the page
 * itself is behind ProtectedRoute like every other /app child (App.tsx).
 */
export function Changelog({ embedded = false }: { embedded?: boolean }) {
  const versionQuery = useQuery({
    queryKey: qk.version(),
    queryFn: () => api.getVersion(),
    staleTime: Infinity,
  });
  const changelogQuery = useQuery({
    queryKey: qk.changelog(),
    queryFn: () => api.getChangelog(),
    staleTime: Infinity,
  });

  const runningVersion = versionQuery.data?.version ?? null;
  const releases = useMemo<ChangelogRelease[]>(() => {
    if (!changelogQuery.data) return [];
    return parseChangelog(changelogQuery.data);
  }, [changelogQuery.data]);
  const status = useMemo(
    () => classifyReleases(releases, runningVersion),
    [releases, runningVersion],
  );

  const versionChip = runningVersion && (
    <span className="flex items-center gap-2 text-sm text-muted-foreground">
      <span>Running</span>
      <code
        data-testid="running-version"
        className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs text-foreground"
      >
        {runningVersion}
      </code>
    </span>
  );

  return (
    <div>
      {embedded ? (
        <div className="mb-4 flex items-center justify-end">{versionChip}</div>
      ) : (
        <PageToolbar title="Changelog">{versionChip}</PageToolbar>
      )}

      <QueryGate query={changelogQuery} errorMessage="Failed to load changelog.">
        {() => {
          if (releases.length === 0) {
            return (
              <p className="py-8 text-center text-sm text-muted-foreground">
                No changelog entries yet. Generate one with{" "}
                <code className="rounded bg-muted px-1 py-0.5">
                  task changelog
                </code>
                .
              </p>
            );
          }
          return (
            <div className="space-y-4">
              {releases.map((r, i) => (
                <ReleaseCard
                  key={`${r.version}-${i}`}
                  release={r}
                  status={status.get(r) ?? "older"}
                />
              ))}
            </div>
          );
        }}
      </QueryGate>
    </div>
  );
}

function ReleaseCard({
  release,
  status,
}: {
  release: ChangelogRelease;
  status: ReleaseStatus;
}) {
  return (
    <Card className={cn(CARD_HIGHLIGHT[status])}>
      <CardHeader className="flex flex-row items-center justify-between gap-3 space-y-0 pb-3">
        <CardTitle className="flex items-center gap-3">
          <span className="font-mono text-lg">
            {release.unreleased ? "unreleased" : `v${release.version}`}
          </span>
          {release.date && (
            <span className="text-sm font-normal text-muted-foreground">
              {release.date}
            </span>
          )}
        </CardTitle>
        <Badge variant={STATUS_BADGE_VARIANT[status]}>
          {STATUS_LABELS[status]}
        </Badge>
      </CardHeader>
      <CardContent className="space-y-4">
        {release.groups.map((g) => (
          <div key={g.name}>
            <h3 className="mb-1.5 text-sm font-semibold uppercase tracking-wide text-muted-foreground">
              {g.name}
            </h3>
            <ul className="ml-4 list-disc space-y-1 text-sm">
              {g.entries.map((e, i) => (
                <EntryRow key={i} entry={e} />
              ))}
            </ul>
          </div>
        ))}
      </CardContent>
    </Card>
  );
}

function EntryRow({ entry }: { entry: ChangelogEntry }) {
  return (
    <li>
      {entry.scope && (
        <span className="mr-1 font-mono text-xs font-semibold text-primary">
          {entry.scope}:
        </span>
      )}
      <span>{entry.message}</span>
    </li>
  );
}
