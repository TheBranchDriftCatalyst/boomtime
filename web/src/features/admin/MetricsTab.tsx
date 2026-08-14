// MetricsTab — Admin > Metrics (gaka-metrics). A GENERIC rate-metrics
// observability dashboard: it fetches GET /api/v1/admin/metrics (the in-memory
// rolling time-series registry) and renders EVERY series as a rate-over-time
// chart, grouped by subsystem. There is no per-metric wiring here — the group
// derivation and the <RateChart> are generic, so the moment the backend calls
// metrics.Inc("some.new.series") it appears on this page.
//
// Live by design: the snapshot polls on a 5s interval (the same cadence as the
// Jobs tab). Each series shows its current (most-recent-bucket) rate as a
// headline plus the full ~2h line.
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Activity, Cpu, Globe, Router } from "lucide-react";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { EmptyState } from "@/components/EmptyState";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { RateChart, currentRate } from "@/viz/charts/RateChart";
import type { MetricSeries } from "@/types/api";

// ── grouping ────────────────────────────────────────────────────────────────
//
// Series names are dotted (http.requests, jobs.limiter.acquired{kind=…},
// hardcover.calls). We bucket by prefix into operator-meaningful groups. A name
// that matches nothing falls into "Other" so a brand-new metric is never
// dropped — it just shows up ungrouped until someone gives it a home here.
type GroupId = "router" | "limiters" | "external" | "other";

interface GroupDef {
  id: GroupId;
  label: string;
  blurb: string;
  icon: typeof Activity;
  match: (name: string) => boolean;
}

const GROUPS: GroupDef[] = [
  {
    id: "router",
    label: "Router",
    blurb: "HTTP request + error rates across the API router.",
    icon: Router,
    match: (n) => n.startsWith("http."),
  },
  {
    id: "limiters",
    label: "Rate-limiters",
    blurb: "Per-kind background-job concurrency limiter throughput + back-pressure.",
    icon: Cpu,
    match: (n) => n.startsWith("jobs.limiter."),
  },
  {
    id: "external",
    label: "External APIs",
    blurb: "Outbound call rates to third-party services.",
    icon: Globe,
    match: (n) => n.startsWith("hardcover.") || n.startsWith("amazon."),
  },
];

function groupFor(name: string): GroupId {
  return GROUPS.find((g) => g.match(name))?.id ?? "other";
}

const OTHER: GroupDef = {
  id: "other",
  label: "Other",
  blurb: "Uncategorized series (newly instrumented metrics land here).",
  icon: Activity,
  match: () => true,
};

// Shorten a series name for a card title: drop the group prefix, keep the label
// braces. "jobs.limiter.acquired{kind=x}" → "acquired{kind=x}".
function shortName(name: string): string {
  const dot = name.indexOf("{") === -1 ? name : name.slice(0, name.indexOf("{"));
  const label = name.slice(dot.length);
  const parts = dot.split(".");
  return (parts[parts.length - 1] || dot) + label;
}

export function MetricsTab() {
  const { data, isLoading, isError } = useQuery({
    queryKey: qk.adminMetrics(),
    queryFn: () => api.getAdminMetrics(),
    refetchInterval: 5000,
  });

  const grouped = useMemo(() => {
    const by: Record<GroupId, MetricSeries[]> = {
      router: [],
      limiters: [],
      external: [],
      other: [],
    };
    for (const s of data ?? []) by[groupFor(s.name)].push(s);
    // Stable ordering within a group.
    for (const k of Object.keys(by) as GroupId[])
      by[k].sort((a, b) => a.name.localeCompare(b.name));
    return by;
  }, [data]);

  if (isLoading) {
    return (
      <div className="mx-auto max-w-6xl p-6 text-sm text-muted-foreground">
        Loading metrics…
      </div>
    );
  }
  if (isError) {
    return (
      <div className="mx-auto max-w-6xl p-6">
        <EmptyState
          icon={Activity}
          title="Couldn’t load metrics"
          description="The metrics endpoint returned an error. It is admin-only; confirm you’re signed in as an admin."
        />
      </div>
    );
  }

  const total = data?.length ?? 0;
  if (total === 0) {
    return (
      <div className="mx-auto max-w-6xl p-6">
        <EmptyState
          icon={Activity}
          title="No metrics yet"
          description="Series appear as soon as instrumented code runs — hit an endpoint, run a job, or make an external API call, then this refreshes every 5s."
        />
      </div>
    );
  }

  const orderedGroups = [...GROUPS, OTHER].filter(
    (g) => grouped[g.id].length > 0,
  );

  return (
    <div className="mx-auto max-w-6xl space-y-8 p-6">
      <p className="text-xs text-muted-foreground">
        In-memory rate registry · {total} series · ~2h window · refreshing every
        5s. Any backend <code className="font-mono">metrics.Inc(name)</code>{" "}
        appears here automatically.
      </p>

      {orderedGroups.map((group) => (
        <section key={group.id} aria-label={group.label} className="space-y-3">
          <header className="flex items-center gap-2">
            <group.icon className="h-4 w-4 text-primary" />
            <h2 className="font-mono text-sm font-semibold uppercase tracking-wider">
              {group.label}
            </h2>
            <span className="text-xs text-muted-foreground">{group.blurb}</span>
          </header>
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
            {grouped[group.id].map((series, i) => (
              <SeriesCard key={series.name} series={series} colorIndex={i} />
            ))}
          </div>
        </section>
      ))}
    </div>
  );
}

function SeriesCard({
  series,
  colorIndex,
}: {
  series: MetricSeries;
  colorIndex: number;
}) {
  const now = currentRate(series);
  const unit = series.kind === "gauge" ? series.unit ?? "" : "/min";
  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex items-baseline justify-between gap-2">
          <CardTitle
            className="truncate font-mono text-xs font-medium"
            title={series.name}
          >
            {shortName(series.name)}
          </CardTitle>
          <span className="shrink-0 font-mono text-sm tabular-nums text-foreground">
            {now.toLocaleString()}
            <span className="ml-0.5 text-[10px] text-muted-foreground">
              {unit}
            </span>
          </span>
        </div>
      </CardHeader>
      <CardContent className="pb-3">
        <RateChart series={series} colorIndex={colorIndex} height={110} />
      </CardContent>
    </Card>
  );
}
