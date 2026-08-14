// ReadingMonitorPanel — Admin › Books › Reading monitor (gaka-books). A THIN
// control over the SERVER-side persistent reading monitor. The poll engine no
// longer runs in this browser tab: it lives server-side, watches each
// in-progress Kindle book's furthest-page-read, and toasts you on a reading
// ping. This panel just (a) flips the engine on/off, (b) switches the toast
// mode, (c) shows a light live status polled from the server, and (d) deep-links
// to the full cadence report in Grafana. Closing the tab does NOT stop the
// monitor — it keeps running server-side.
import { BookOpenCheck, ExternalLink, Gauge, Radio } from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Switch } from "@thebranchdriftcatalyst/catalyst-ui/ui/switch";
import { Label } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { relativeTime } from "@/lib/sourceStatus";
import { cn } from "@/lib/utils";
import type {
  ReadingMonitorMode,
  ReadingMonitorRecommendation,
  ReadingMonitorState,
} from "@/types/api";

// Grafana deep-link (gaka-books). The cadence board's stable uid is
// `boomtime-reading-monitor`. Base URL: VITE_GRAFANA_BASE_URL when set (Grafana
// on its own origin), else a same-host `/grafana` reverse-proxy path — the
// common self-hosted default. Trailing slash trimmed so the join is clean.
const CADENCE_BOARD_UID = "boomtime-reading-monitor";
const GRAFANA_BASE = (
  import.meta.env.VITE_GRAFANA_BASE_URL ?? "/grafana"
).replace(/\/$/, "");
const CADENCE_BOARD_URL = `${GRAFANA_BASE}/d/${CADENCE_BOARD_UID}`;

// Poll the status endpoint gently while the panel is open. This is display-only
// — the actual monitor cadence runs server-side, not off this timer.
const STATUS_POLL_MS = 15_000;

const MODES: {
  id: ReadingMonitorMode;
  label: string;
  hint: string;
}[] = [
  {
    id: "debounced",
    label: "Debounced",
    hint: "One toast per book advance — a quiet nudge each time a book moves forward.",
  },
  {
    id: "verbose",
    label: "Verbose",
    hint: "A toast on every ping — the raw firehose of each reading sample.",
  },
];

// A pulsing status dot: green + ping when running, grey when off.
function LiveDot({ on }: { on: boolean }) {
  return (
    <span className="relative flex h-2.5 w-2.5">
      {on && (
        <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400/70" />
      )}
      <span
        className={cn(
          "relative inline-flex h-2.5 w-2.5 rounded-full",
          on ? "bg-emerald-400" : "bg-muted-foreground/50",
        )}
      />
    </span>
  );
}

function StatTile({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="rounded-md border border-border bg-muted/20 p-3">
      <div className="text-[11px] uppercase tracking-wide text-muted-foreground">
        {label}
      </div>
      <div className="mt-1 font-mono text-lg font-semibold">{value}</div>
      {hint && <div className="text-[11px] text-muted-foreground">{hint}</div>}
    </div>
  );
}

// The headline ANSWER to "what's the optimal polling timeframe" — stated in
// plain English right on the page, derived from observed advances. Null (too
// little data) renders the calibrating fallback so the panel never goes silent.
function RecommendationBlock({
  recommendation,
  loading,
}: {
  recommendation: ReadingMonitorRecommendation | null | undefined;
  loading: boolean;
}) {
  const rec = recommendation ?? null;
  return (
    <div className="rounded-lg border border-primary/40 bg-primary/5 p-4">
      <div className="flex items-center gap-1.5 text-[11px] font-medium uppercase tracking-wide text-primary">
        <Gauge className="h-3.5 w-3.5" />
        Optimal polling
      </div>
      {rec ? (
        <>
          <p className="mt-1.5 text-sm leading-relaxed" data-testid="reading-monitor-recommendation">
            Optimal polling (from{" "}
            <span className="font-mono font-semibold">{rec.sampleCount}</span>{" "}
            observed advances): detect all books every{" "}
            <span className="font-mono font-semibold">~{rec.detectSecs}s</span>,
            fast-capture an active book every{" "}
            <span className="font-mono font-semibold">~{rec.captureSecs}s</span>,
            mark idle after{" "}
            <span className="font-mono font-semibold">~{rec.idleSecs}s</span> of
            no advance. Your whispersync pushes at a median of{" "}
            <span className="font-mono font-semibold">
              ~{rec.medianAdvanceSecs}s
            </span>{" "}
            (p90{" "}
            <span className="font-mono font-semibold">~{rec.p90AdvanceSecs}s</span>).
          </p>
          <p className="mt-1 text-[11px] text-muted-foreground">
            Full cadence analysis + history in Grafana.
          </p>
        </>
      ) : (
        <p
          className="mt-1.5 text-sm leading-relaxed text-muted-foreground"
          data-testid="reading-monitor-recommendation-empty"
        >
          {loading
            ? "Loading calibration…"
            : "Not enough data yet — turn the monitor on and read a Kindle book to calibrate the optimal intervals."}
        </p>
      )}
    </div>
  );
}

export function ReadingMonitorPanel() {
  const qc = useQueryClient();

  const { data, isLoading, isError } = useQuery({
    queryKey: qk.readingMonitor(),
    queryFn: () => api.getReadingMonitor(),
    refetchInterval: STATUS_POLL_MS,
  });

  const mutate = useMutation({
    mutationFn: (body: { enabled?: boolean; mode?: ReadingMonitorMode }) =>
      api.setReadingMonitor(body),
    // Write the authoritative server response straight into the cache so the
    // switch + mode reflect server truth without waiting for the next poll.
    onSuccess: (next: ReadingMonitorState, vars) => {
      qc.setQueryData(qk.readingMonitor(), next);
      if (vars.enabled !== undefined) {
        toast.success(
          next.enabled
            ? "Reading monitor started — running server-side"
            : "Reading monitor stopped",
        );
      } else if (vars.mode !== undefined) {
        toast.success(`Toast mode: ${next.mode}`);
      }
    },
    onError: (e) => {
      toast.error(e instanceof Error ? e.message : "Could not update the monitor");
    },
  });

  const enabled = data?.enabled ?? false;
  const mode = data?.mode ?? "debounced";
  const busy = isLoading || mutate.isPending;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex items-center gap-2">
          <Radio className="h-5 w-5 text-primary" />
          <h2 className="text-lg font-semibold">Books · reading monitor</h2>
          <span
            className={cn(
              "flex items-center gap-1.5 rounded px-2 py-0.5 text-[11px] font-medium uppercase tracking-wide",
              enabled
                ? "bg-emerald-500/15 text-emerald-400"
                : "bg-muted/50 text-muted-foreground",
            )}
          >
            <LiveDot on={enabled} />
            {enabled ? "running" : "off"}
          </span>
        </div>
      </div>

      <p className="text-sm text-muted-foreground">
        The monitor runs <span className="font-medium">server-side</span> and
        toasts you when a reading ping lands — turn it on and you can close this
        tab. The full cadence report (how often whispersync pushes, how far it
        jumps) lives in Grafana.
      </p>

      {isError && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          Couldn&apos;t reach the reading monitor. Retrying…
        </div>
      )}

      {/* The plain-English optimal-polling ANSWER — the star of the panel. */}
      <RecommendationBlock recommendation={data?.recommendation} loading={isLoading} />

      {/* On/off toggle — the prominent primary control. */}
      <div className="flex items-center justify-between rounded-lg border border-border bg-muted/10 p-4">
        <div className="space-y-0.5">
          <Label htmlFor="reading-monitor-enabled" className="text-base font-semibold">
            Reading monitor: {enabled ? "ON" : "OFF"}
          </Label>
          <p className="text-xs text-muted-foreground">
            {enabled
              ? "Running server-side — you can close this tab and it keeps watching."
              : "Off — no reading pings are being watched."}
          </p>
        </div>
        <Switch
          id="reading-monitor-enabled"
          data-testid="reading-monitor-switch"
          checked={enabled}
          disabled={busy}
          onCheckedChange={(checked) => mutate.mutate({ enabled: checked })}
        />
      </div>

      {/* Toast-mode switch: Debounced ↔ Verbose. */}
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border p-4">
        <div className="space-y-0.5">
          <div className="text-sm font-medium">Toast mode</div>
          <p className="text-xs text-muted-foreground">
            How chatty the reading-ping toasts are.
          </p>
        </div>
        <div
          className="flex rounded-md border border-border p-0.5"
          role="group"
          aria-label="Toast mode"
        >
          {MODES.map((m) => (
            <button
              key={m.id}
              type="button"
              title={m.hint}
              aria-pressed={mode === m.id}
              disabled={busy}
              onClick={() => mutate.mutate({ mode: m.id })}
              className={cn(
                "rounded px-3 py-1 text-sm transition-colors disabled:opacity-50",
                mode === m.id
                  ? "bg-primary text-primary-foreground"
                  : "text-muted-foreground hover:text-foreground",
              )}
            >
              {m.label}
            </button>
          ))}
        </div>
      </div>

      {/* Live status (polled from the server for display only). */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
        <StatTile
          label="Active books"
          value={data ? String(data.activeBooks) : "—"}
          hint="in-progress, watched"
        />
        <StatTile
          label="Last ping"
          value={data?.lastPingAt ? relativeTime(data.lastPingAt) : "—"}
          hint={data?.lastPingAt ? undefined : "no ping yet"}
        />
      </div>

      {/* Grafana deep-link + explanatory line. */}
      <div className="flex flex-wrap items-center gap-3">
        <Button asChild size="sm" variant="outline">
          <a href={CADENCE_BOARD_URL} target="_blank" rel="noopener noreferrer">
            <BookOpenCheck className="mr-2 h-4 w-4" />
            Open cadence dashboard
            <ExternalLink className="ml-2 h-3.5 w-3.5" />
          </a>
        </Button>
        <span className="text-xs text-muted-foreground">
          Full whispersync cadence report in Grafana.
        </span>
      </div>
    </div>
  );
}
