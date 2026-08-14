// ReadingMonitorPanel — Admin › Books › Reading monitor (gaka-books). A LIVE
// diagnostic: hit Start, open + read a book on your Kindle/iPad, and watch the
// furthest-page-read advance in real time. The panel opens a WebSocket that
// polls each in-progress Kindle book's last-page-read position at a high sample
// rate and streams every advance here — so we can empirically measure the
// whispersync sync cadence (how often it pushes, how far it jumps) and decide
// gap-sum vs position-delta for the reading-time composition.
//
// Read-only: nothing observed here is persisted server-side.
import { useMemo, useState } from "react";
import {
  Activity,
  AlertTriangle,
  BookOpenCheck,
  Gauge,
  Play,
  Radio,
  RotateCcw,
  Square,
} from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { EmptyState } from "@/components/EmptyState";
import { cn } from "@/lib/utils";
import {
  useReadingMonitorSocket,
  type SocketStatus,
} from "./useReadingMonitorSocket";
import {
  buildRows,
  computeCadence,
  fmtDeltaLocation,
  fmtInterval,
  type MonitorRow,
} from "./readingMonitorCadence";

const DEFAULT_INTERVAL = 6;
const DEFAULT_LIMIT = 12;

function statusLabel(status: SocketStatus, running: boolean): string {
  if (!running) return "idle";
  switch (status) {
    case "open":
      return "live";
    case "connecting":
      return "connecting";
    case "reconnecting":
      return "reconnecting";
    default:
      return "closed";
  }
}

// A pulsing status dot: green+pulse when live, amber while (re)connecting, grey idle.
function LiveDot({ status, running }: { status: SocketStatus; running: boolean }) {
  const live = running && status === "open";
  const connecting =
    running && (status === "connecting" || status === "reconnecting");
  return (
    <span className="relative flex h-2.5 w-2.5">
      {live && (
        <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-400/70" />
      )}
      <span
        className={cn(
          "relative inline-flex h-2.5 w-2.5 rounded-full",
          live && "bg-emerald-400",
          connecting && "bg-amber-400",
          !live && !connecting && "bg-muted-foreground/50",
        )}
      />
    </span>
  );
}

// A tiny dependency-free sparkline of one book's position over its samples.
function Sparkline({ values }: { values: number[] }) {
  if (values.length < 2) return null;
  const w = 120;
  const h = 24;
  const min = Math.min(...values);
  const max = Math.max(...values);
  const span = max - min || 1;
  const pts = values
    .map((v, i) => {
      const x = (i / (values.length - 1)) * w;
      const y = h - ((v - min) / span) * h;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
  return (
    <svg
      width={w}
      height={h}
      viewBox={`0 0 ${w} ${h}`}
      className="text-primary"
      preserveAspectRatio="none"
      aria-hidden
    >
      <polyline
        points={pts}
        fill="none"
        stroke="currentColor"
        strokeWidth={1.5}
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </svg>
  );
}

function StatTile({
  icon: Icon,
  label,
  value,
  hint,
}: {
  icon: typeof Gauge;
  label: string;
  value: string;
  hint?: string;
}) {
  return (
    <div className="rounded-md border border-border bg-muted/20 p-3">
      <div className="flex items-center gap-1.5 text-[11px] uppercase tracking-wide text-muted-foreground">
        <Icon className="h-3.5 w-3.5" />
        {label}
      </div>
      <div className="mt-1 font-mono text-lg font-semibold">{value}</div>
      {hint && <div className="text-[11px] text-muted-foreground">{hint}</div>}
    </div>
  );
}

function timeOnly(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime())
    ? iso
    : d.toLocaleTimeString(undefined, { hour12: false });
}

export function ReadingMonitorPanel() {
  const [running, setRunning] = useState(false);
  const [interval, setIntervalSec] = useState(DEFAULT_INTERVAL);
  const [limit, setLimit] = useState(DEFAULT_LIMIT);

  const stream = useReadingMonitorSocket({
    enabled: running,
    intervalSec: interval,
    limit,
  });

  const rows = useMemo(() => buildRows(stream.samples), [stream.samples]);
  const cadence = useMemo(() => computeCadence(rows), [rows]);

  // Newest-on-top for the table.
  const rowsDesc = useMemo(() => [...rows].reverse(), [rows]);

  // Per-book position series for the sparklines (chronological).
  const series = useMemo(() => {
    const byAsin = new Map<string, { title: string; values: number[] }>();
    for (const r of rows) {
      const entry = byAsin.get(r.asin) ?? { title: r.title, values: [] };
      entry.title = r.title;
      entry.values.push(r.location);
      byAsin.set(r.asin, entry);
    }
    return Array.from(byAsin.entries())
      .filter(([, e]) => e.values.length >= 2)
      .map(([asin, e]) => ({ asin, ...e }));
  }, [rows]);

  const start = () => {
    stream.clear();
    setRunning(true);
  };
  const stop = () => setRunning(false);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex items-center gap-2">
          <Radio className="h-5 w-5 text-primary" />
          <h2 className="text-lg font-semibold">Books · reading monitor</h2>
          <span
            className={cn(
              "flex items-center gap-1.5 rounded px-2 py-0.5 text-[11px] font-medium uppercase tracking-wide",
              running && stream.status === "open"
                ? "bg-emerald-500/15 text-emerald-400"
                : "bg-muted/50 text-muted-foreground",
            )}
          >
            <LiveDot status={stream.status} running={running} />
            {statusLabel(stream.status, running)}
          </span>
        </div>

        <div className="ml-auto flex flex-wrap items-center gap-2">
          <label className="flex items-center gap-1 text-xs text-muted-foreground">
            interval
            <input
              type="number"
              min={2}
              max={60}
              value={interval}
              disabled={running}
              onChange={(e) => setIntervalSec(Number(e.target.value) || DEFAULT_INTERVAL)}
              className="w-16 rounded-md border border-border bg-transparent px-2 py-1 font-mono text-xs disabled:opacity-50"
            />
            s
          </label>
          <label className="flex items-center gap-1 text-xs text-muted-foreground">
            books
            <input
              type="number"
              min={1}
              max={50}
              value={limit}
              disabled={running}
              onChange={(e) => setLimit(Number(e.target.value) || DEFAULT_LIMIT)}
              className="w-16 rounded-md border border-border bg-transparent px-2 py-1 font-mono text-xs disabled:opacity-50"
            />
          </label>
          <Button
            size="sm"
            variant="outline"
            disabled={running || stream.samples.length === 0}
            onClick={stream.clear}
          >
            <RotateCcw className="mr-2 h-4 w-4" />
            Clear
          </Button>
          {running ? (
            <Button size="sm" variant="destructive" onClick={stop}>
              <Square className="mr-2 h-4 w-4" />
              Stop
            </Button>
          ) : (
            <Button size="sm" onClick={start}>
              <Play className="mr-2 h-4 w-4" />
              Start
            </Button>
          )}
        </div>
      </div>

      <p className="text-sm text-muted-foreground">
        Polls each in-progress Kindle book&apos;s last-page-read position every{" "}
        <span className="font-mono">{interval}s</span> and streams every advance
        below. Start the monitor, then open + read a book on your Kindle or iPad
        and watch the furthest page advance — an empirical probe of the
        whispersync sync cadence. Read-only; nothing here is persisted.
      </p>

      {/* Cadence stats */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
        <StatTile
          icon={Activity}
          label="Advances"
          value={String(cadence.advances)}
          hint={`${cadence.books} book${cadence.books === 1 ? "" : "s"}`}
        />
        <StatTile
          icon={Gauge}
          label="Min interval"
          value={fmtInterval(cadence.minIntervalSec)}
        />
        <StatTile
          icon={Gauge}
          label="Median interval"
          value={fmtInterval(cadence.medianIntervalSec)}
        />
        <StatTile
          icon={BookOpenCheck}
          label="Avg Δloc"
          value={
            cadence.avgDeltaLocation === undefined
              ? "—"
              : `+${Math.round(cadence.avgDeltaLocation)}`
          }
          hint="per advance"
        />
        <StatTile
          icon={Gauge}
          label="Sec / location"
          value={
            cadence.secondsPerLocation === undefined
              ? "—"
              : cadence.secondsPerLocation.toFixed(2)
          }
          hint="implied"
        />
      </div>

      {/* Per-book sparklines */}
      {series.length > 0 && (
        <div className="flex flex-wrap gap-4">
          {series.map((s) => (
            <div
              key={s.asin}
              className="rounded-md border border-border bg-muted/10 px-3 py-2"
            >
              <div className="max-w-[160px] truncate text-xs font-medium" title={s.title}>
                {s.title}
              </div>
              <Sparkline values={s.values} />
              <div className="font-mono text-[11px] text-muted-foreground">
                {s.values[s.values.length - 1]}
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Error frames (non-fatal) */}
      {stream.errors.length > 0 && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          <div className="mb-1 flex items-center gap-1.5 font-medium">
            <AlertTriangle className="h-3.5 w-3.5" />
            {stream.errors.length} stream warning
            {stream.errors.length === 1 ? "" : "s"}
          </div>
          <div className="font-mono">{stream.errors[stream.errors.length - 1]}</div>
        </div>
      )}

      {/* Live sample table */}
      <SampleTable
        rows={rowsDesc}
        running={running}
        hasSamples={stream.samples.length > 0}
        lastHeartbeatAt={stream.lastHeartbeat?.sampledAt}
      />
    </div>
  );
}

function SampleTable({
  rows,
  running,
  hasSamples,
  lastHeartbeatAt,
}: {
  rows: MonitorRow[];
  running: boolean;
  hasSamples: boolean;
  lastHeartbeatAt?: string;
}) {
  if (!running && !hasSamples) {
    return (
      <EmptyState
        icon={Radio}
        title="Monitor idle"
        description="Start the monitor, then open + read a book on your Kindle to watch the furthest-page-read advance in real time."
      />
    );
  }
  if (hasSamples === false) {
    return (
      <EmptyState
        icon={BookOpenCheck}
        title="Waiting for the first advance…"
        description={
          lastHeartbeatAt
            ? `Stream is live (last heartbeat ${timeOnly(lastHeartbeatAt)}). Turn a page in your open book and it will appear here.`
            : "Stream is live. Open a book and turn a page — advances appear here newest-first."
        }
      />
    );
  }
  return (
    <div className="overflow-x-auto rounded-md border border-border">
      <table className="w-full text-xs">
        <thead className="bg-muted/40">
          <tr>
            {["time", "book", "location", "Δloc", "creationTime", "Δt"].map((c) => (
              <th
                key={c}
                className="whitespace-nowrap px-2 py-1 text-left font-mono font-medium"
              >
                {c}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.seq} className="border-t border-border/60">
              <td className="whitespace-nowrap px-2 py-1 font-mono text-muted-foreground">
                {timeOnly(r.sampledAt)}
              </td>
              <td className="max-w-[220px] truncate px-2 py-1" title={r.title}>
                {r.title}
              </td>
              <td className="px-2 py-1 font-mono">{r.location}</td>
              <td
                className={cn(
                  "px-2 py-1 font-mono",
                  r.deltaLocation && r.deltaLocation > 0 && "text-emerald-400",
                  r.deltaLocation && r.deltaLocation < 0 && "text-destructive",
                )}
              >
                {fmtDeltaLocation(r.deltaLocation)}
              </td>
              <td className="whitespace-nowrap px-2 py-1 font-mono text-muted-foreground">
                {r.creationTime ? timeOnly(r.creationTime) : "—"}
              </td>
              <td className="px-2 py-1 font-mono text-muted-foreground">
                {fmtInterval(r.deltaSeconds)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
