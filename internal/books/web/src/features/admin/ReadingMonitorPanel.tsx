// ReadingMonitorPanel — Admin › Books › Reading monitor (gaka-books). A THIN
// control over the SERVER-side persistent reading monitor. The poll engine no
// longer runs in this browser tab: it lives server-side, watches each
// in-progress Kindle book's furthest-page-read, and toasts you on a reading
// ping. This panel just (a) flips the engine on/off, (b) switches the toast
// mode, (c) shows a light live status polled from the server, and (d) deep-links
// to the full cadence report in Grafana. Closing the tab does NOT stop the
// monitor — it keeps running server-side.
import { useEffect, useState } from "react";
import {
  BookOpenCheck,
  ExternalLink,
  Gauge,
  Radar,
  Radio,
  Square,
  Timer,
} from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Switch } from "@thebranchdriftcatalyst/catalyst-ui/ui/switch";
import { Label } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";
import { AdminTabShell } from "@shared/shared/admin/AdminTabShell";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import { relativeTime } from "@shared/lib/sourceStatus";
import { cn } from "@shared/lib/utils";
import type {
  ReadingMonitorMode,
  ReadingMonitorRecommendation,
  ReadingMonitorState,
} from "@shared/types/api";

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
          {/* rm2 · the classification the calibration window fingerprinted: the
              observed sync SHAPE → the cadence-measurement method it implies. */}
          <p
            className="mt-2 rounded-md border border-primary/30 bg-background/40 px-2.5 py-2 text-[13px] leading-relaxed"
            data-testid="reading-monitor-classification"
          >
            <span className="font-medium text-primary">Observed sync pattern:</span>{" "}
            <span className="font-mono font-semibold uppercase tracking-wide">
              {rec.syncPattern}
            </span>{" "}
            <span className="text-muted-foreground">→</span>{" "}
            <span className="font-mono font-semibold">{rec.impliedMethod}</span>{" "}
            method.{rec.rationale ? <> {rec.rationale}</> : null}
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

// A 1-Hz clock, live only while `active` — powers the calibration countdown
// without leaving a timer running once the window closes.
function useNow(active: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    setNow(Date.now());
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [active]);
  return now;
}

// "~7m left" / "~45s left" / "expiring…" — the human read-out of how long the
// high-fidelity window has to run. Null `until` (or a past instant) reads as
// expiring so the UI never shows a negative or stale time.
function formatRemaining(until: string | null, now: number): string {
  if (!until) return "expiring…";
  const ms = new Date(until).getTime() - now;
  if (!Number.isFinite(ms) || ms <= 0) return "expiring…";
  const secs = Math.round(ms / 1000);
  if (secs < 90) return `~${secs}s left`;
  return `~${Math.ceil(secs / 60)}m left`;
}

// The star of rm2: a prominent, distinctly-styled control that starts / stops
// the temporary HIGH-FIDELITY calibration window. Idle → a single amber CTA.
// Active → a pulsing amber panel with a live countdown + the "read a Kindle
// book now" instruction, so it unmistakably reads as a special burning state.
function DiagnosticModeCard({
  calibrating,
  calibratingUntil,
  busy,
  onStart,
  onStop,
}: {
  calibrating: boolean;
  calibratingUntil: string | null;
  busy: boolean;
  onStart: () => void;
  onStop: () => void;
}) {
  const now = useNow(calibrating);

  if (!calibrating) {
    return (
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-amber-500/40 bg-amber-500/[0.04] p-4">
        <div className="flex items-start gap-3">
          <Radar className="mt-0.5 h-5 w-5 shrink-0 text-amber-400" />
          <div className="space-y-0.5">
            <div className="text-sm font-semibold">Diagnostic (calibration) mode</div>
            <p className="max-w-prose text-xs text-muted-foreground">
              Opens a short <span className="font-medium">high-fidelity</span>{" "}
              window (~10s polling for ~20&nbsp;min) to measure your{" "}
              <span className="font-medium">true</span> whispersync cadence — far
              finer than the normal 60–120s poll. It auto-expires, and it burns
              Amazon calls while it runs.
            </p>
          </div>
        </div>
        <Button
          size="sm"
          data-testid="diagnostic-mode-start"
          disabled={busy}
          onClick={onStart}
          className="border border-amber-400/60 bg-amber-500/15 text-amber-300 hover:bg-amber-500/25"
        >
          <Radar className="mr-2 h-4 w-4" />
          Start Diagnostic Mode
        </Button>
      </div>
    );
  }

  // ACTIVE — pulsing amber panel. A dedicated glow layer animates so the text
  // stays crisp while the surround breathes.
  return (
    <div
      data-testid="diagnostic-mode-active"
      className="relative overflow-hidden rounded-lg border border-amber-400/70 bg-amber-500/[0.06] p-4"
      style={{
        boxShadow:
          "0 0 0 1px color-mix(in oklab, oklch(0.82 0.16 78) 45%, transparent), 0 0 22px color-mix(in oklab, oklch(0.82 0.16 78) 30%, transparent)",
      }}
    >
      <span
        aria-hidden
        className="pointer-events-none absolute inset-0 animate-pulse"
        style={{
          background:
            "radial-gradient(120% 80% at 12% 0%, color-mix(in oklab, oklch(0.82 0.16 78) 22%, transparent), transparent 60%)",
        }}
      />
      <div className="relative flex flex-wrap items-start justify-between gap-3">
        <div className="flex items-start gap-3">
          <span className="relative mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center">
            <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-amber-400/50" />
            <Radar className="relative h-5 w-5 text-amber-300" />
          </span>
          <div className="space-y-1">
            <div className="flex items-center gap-2 text-sm font-semibold text-amber-200">
              High-fidelity calibration active
              <span className="inline-flex items-center gap-1 rounded bg-amber-500/20 px-1.5 py-0.5 font-mono text-[11px] font-medium text-amber-200">
                <Timer className="h-3 w-3" />
                <span data-testid="diagnostic-mode-countdown">
                  {formatRemaining(calibratingUntil, now)}
                </span>
              </span>
            </div>
            <p className="max-w-prose text-xs text-amber-100/80">
              Read a Kindle book now to measure your true sync cadence — the
              window is polling at ~10s and auto-expires. Each poll spends an
              Amazon call, so stop it once you&apos;re done reading.
            </p>
          </div>
        </div>
        <Button
          size="sm"
          variant="outline"
          data-testid="diagnostic-mode-stop"
          disabled={busy}
          onClick={onStop}
          className="border-amber-400/60 text-amber-200 hover:bg-amber-500/15"
        >
          <Square className="mr-2 h-3.5 w-3.5" />
          Stop
        </Button>
      </div>
    </div>
  );
}

export function ReadingMonitorPanel() {
  const qc = useQueryClient();

  const { data, isLoading, isError } = useQuery({
    queryKey: qk.readingMonitor(),
    queryFn: () => api.getReadingMonitor(),
    // Poll harder while a calibration window is live so the countdown + the
    // auto-expiry flip land promptly; back off to the gentle cadence otherwise.
    refetchInterval: (query) =>
      query.state.data?.calibrating ? 5_000 : STATUS_POLL_MS,
  });

  const mutate = useMutation({
    mutationFn: (body: {
      enabled?: boolean;
      mode?: ReadingMonitorMode;
      calibrate?: boolean;
    }) => api.setReadingMonitor(body),
    // Write the authoritative server response straight into the cache so the
    // switch + mode reflect server truth without waiting for the next poll.
    onSuccess: (next: ReadingMonitorState, vars) => {
      qc.setQueryData(qk.readingMonitor(), next);
      // rm2 · calibrate PUTs also refresh the raw feed + the nav-indicator
      // beacon so both reflect the new window immediately.
      qc.invalidateQueries({ queryKey: qk.readingMonitorRaw() });
      qc.invalidateQueries({ queryKey: qk.readingMonitorStatus() });
      if (vars.calibrate !== undefined) {
        toast.success(
          next.calibrating
            ? "Diagnostic mode started — high-fidelity calibration running"
            : "Diagnostic mode stopped",
        );
      } else if (vars.enabled !== undefined) {
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

  // Renders through the shared AdminTabShell base (gaka-zp2s); this panel keeps
  // its own inline isError banner + live-status chrome inside the shell body.
  return (
    <AdminTabShell bodyClassName="space-y-4">
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

      {/* rm2 · Diagnostic (calibration) mode — start/stop the high-fidelity
          window; glows + counts down while active. */}
      <DiagnosticModeCard
        calibrating={data?.calibrating ?? false}
        calibratingUntil={data?.calibratingUntil ?? null}
        busy={busy}
        onStart={() => mutate.mutate({ calibrate: true })}
        onStop={() => mutate.mutate({ calibrate: false })}
      />

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
    </AdminTabShell>
  );
}
