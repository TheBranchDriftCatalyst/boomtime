// MetricsTab — Admin > Metrics (gaka-metrics, pivoted to Prometheus). The
// backend now exports Prometheus at /metrics (scraped by the cluster Prometheus
// → Grafana); this tab is the lightweight IN-APP view over the SAME registry.
// It fetches GET /api/v1/admin/metrics (Registry.Gather() flattened to
// {families:[{name,help,type,samples}]}) and renders every family grouped by
// subsystem, with each label-set's current value.
//
// There is no per-metric wiring: the grouping is by name prefix and the sample
// rendering is generic, so a newly-registered collector shows up automatically
// (in "Other" until it's given a group here). Live by design — polls every 5s.
//
// Note this shows INSTANTANEOUS values (counters are cumulative totals,
// histograms show count + derived average latency). Rate-over-time + alerting
// live in Grafana over the /metrics scrape; this tab is the "is this pod doing
// what I expect right now" glance.
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { Activity, Cpu, Globe, Router, Send, Server } from "lucide-react";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { EmptyState } from "@/components/EmptyState";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import type { MetricFamily, MetricSample } from "@/types/api";

// ── grouping ────────────────────────────────────────────────────────────────
type GroupId =
  | "router"
  | "outbound"
  | "limiters"
  | "external"
  | "runtime"
  | "other";

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
    label: "Router (incoming)",
    blurb: "HTTP requests + latency served by the API router (RED, by route template).",
    icon: Router,
    match: (n) => n.startsWith("http_request"),
  },
  {
    id: "outbound",
    label: "Outbound HTTP",
    blurb: "Every external call boomtime makes, by target host (RED).",
    icon: Send,
    match: (n) => n.startsWith("http_client_"),
  },
  {
    id: "limiters",
    label: "Rate-limiters",
    blurb: "Per-kind background-job concurrency limiter throughput + back-pressure.",
    icon: Cpu,
    match: (n) => n.startsWith("jobs_limiter_"),
  },
  {
    id: "external",
    label: "External APIs",
    blurb: "Semantic per-service call counters (outcome / transport dimensions).",
    icon: Globe,
    match: (n) => n.startsWith("hardcover_") || n.startsWith("amazon_"),
  },
  {
    id: "runtime",
    label: "Runtime",
    blurb: "Go runtime + process collectors (goroutines, GC, heap, FDs, CPU).",
    icon: Server,
    match: (n) => n.startsWith("go_") || n.startsWith("process_"),
  },
];

function groupFor(name: string): GroupId {
  return GROUPS.find((g) => g.match(name))?.id ?? "other";
}

const OTHER: GroupDef = {
  id: "other",
  label: "Other",
  blurb: "Uncategorized families (newly registered collectors land here).",
  icon: Activity,
  match: () => true,
};

// ── sample value helpers ─────────────────────────────────────────────────────

// primaryValue is the number used to sort + size a sample within its family:
// the counter/gauge value, or a histogram/summary's observation count.
function primaryValue(s: MetricSample): number {
  if (typeof s.value === "number") return s.value;
  if (typeof s.count === "number") return s.count;
  return 0;
}

// formatSample renders a sample's headline: a plain number for counters/gauges,
// or "N · avg X ms" for histograms/summaries (avg = sum/count, seconds→ms).
function formatSample(s: MetricSample): string {
  if (typeof s.value === "number") return s.value.toLocaleString();
  if (typeof s.count === "number") {
    const n = s.count.toLocaleString();
    if (typeof s.sum === "number" && s.count > 0) {
      const avgMs = (s.sum / s.count) * 1000;
      return `${n} · avg ${avgMs.toFixed(avgMs < 10 ? 1 : 0)} ms`;
    }
    return n;
  }
  return "0";
}

// labelText renders a sample's label set as "k=v · k=v" (sorted for stability),
// or "—" for an unlabelled sample (e.g. go_goroutines).
function labelText(labels?: Record<string, string>): string {
  if (!labels) return "—";
  const keys = Object.keys(labels).sort();
  if (keys.length === 0) return "—";
  return keys.map((k) => `${k}=${labels[k]}`).join(" · ");
}

export function MetricsTab() {
  const { data, isLoading, isError } = useQuery({
    queryKey: qk.adminMetrics(),
    queryFn: () => api.getAdminMetrics(),
    refetchInterval: 5000,
  });

  const grouped = useMemo(() => {
    const by: Record<GroupId, MetricFamily[]> = {
      router: [],
      outbound: [],
      limiters: [],
      external: [],
      runtime: [],
      other: [],
    };
    for (const f of data ?? []) by[groupFor(f.name)].push(f);
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
          description="Families appear as soon as instrumented code runs — hit an endpoint, run a job, or make an external API call, then this refreshes every 5s."
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
        Prometheus registry · {total} families · refreshing every 5s. Also
        scraped at <code className="font-mono">/metrics</code> for Grafana. Any
        newly-registered collector appears here automatically.
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
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            {grouped[group.id].map((family) => (
              <FamilyCard key={family.name} family={family} />
            ))}
          </div>
        </section>
      ))}
    </div>
  );
}

function FamilyCard({ family }: { family: MetricFamily }) {
  // Sort samples by value desc so the hottest label-set reads first; cap the
  // list so a high-cardinality family (many hosts/routes) stays glanceable.
  const samples = useMemo(() => {
    const sorted = [...family.samples].sort(
      (a, b) => primaryValue(b) - primaryValue(a),
    );
    return sorted;
  }, [family.samples]);

  const max = samples.length > 0 ? primaryValue(samples[0]) || 1 : 1;
  const shown = samples.slice(0, 12);
  const hidden = samples.length - shown.length;

  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex items-baseline justify-between gap-2">
          <CardTitle
            className="truncate font-mono text-xs font-medium"
            title={family.help || family.name}
          >
            {family.name}
          </CardTitle>
          <span className="shrink-0 font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
            {family.type}
          </span>
        </div>
      </CardHeader>
      <CardContent className="space-y-1.5 pb-3">
        {shown.length === 0 ? (
          <p className="text-xs text-muted-foreground">no samples</p>
        ) : (
          shown.map((s, i) => {
            const pct = Math.max(2, (primaryValue(s) / max) * 100);
            return (
              <div key={i} className="relative">
                <div
                  className="absolute inset-y-0 left-0 rounded-sm bg-primary/10"
                  style={{ width: `${pct}%` }}
                  aria-hidden
                />
                <div className="relative flex items-center justify-between gap-3 px-1.5 py-0.5">
                  <span
                    className="truncate font-mono text-[11px] text-muted-foreground"
                    title={labelText(s.labels)}
                  >
                    {labelText(s.labels)}
                  </span>
                  <span className="shrink-0 font-mono text-xs tabular-nums text-foreground">
                    {formatSample(s)}
                  </span>
                </div>
              </div>
            );
          })
        )}
        {hidden > 0 && (
          <p className="pt-1 text-[10px] text-muted-foreground">
            +{hidden} more label sets
          </p>
        )}
      </CardContent>
    </Card>
  );
}
