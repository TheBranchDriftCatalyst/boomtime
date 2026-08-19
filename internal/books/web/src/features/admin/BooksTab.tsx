// BooksTab — Admin › Books (gaka-books). A DIAGNOSTIC dump of everything the
// Audible / Kindle sources return for the admin's connected Amazon account, so
// we can inventory every available metric/field before committing the model.
// Each "probe" is a raw signed request; we show its status + the verbatim body
// (parsed JSON with a field table, or raw text for XML/error pages).
import { useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { AdminTabShell } from "@/shared/admin/AdminTabShell";
import {
  AlertTriangle,
  BookOpen,
  CheckCircle2,
  Headphones,
  Loader2,
  Play,
  Radar,
  Radio,
  RefreshCw,
  Tablet,
} from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { relativeTime } from "@/lib/sourceStatus";
import { ReadingMonitorPanel } from "./ReadingMonitorPanel";

interface Probe {
  name: string;
  endpoint: string;
  status: number;
  ok: boolean;
  error?: string;
  body?: unknown;
  bodyText?: string;
}
interface DiagResult {
  source: string;
  marketplace: string;
  probes: Probe[];
}

const SOURCES = [
  { id: "audible", label: "Audible" },
  { id: "kindle", label: "Kindle" },
] as const;

// Pull the array of records a probe returned, if any (Audible → body.items,
// whispersync → body.datasets, else the body itself if it's an array).
function recordsOf(body: unknown): Record<string, unknown>[] | null {
  if (Array.isArray(body)) return body as Record<string, unknown>[];
  if (body && typeof body === "object") {
    const o = body as Record<string, unknown>;
    for (const k of ["items", "datasets", "results", "records"]) {
      if (Array.isArray(o[k])) return o[k] as Record<string, unknown>[];
    }
  }
  return null;
}

// Union of top-level scalar keys across records → the "every metric" columns.
function scalarColumns(records: Record<string, unknown>[]): string[] {
  const keys = new Set<string>();
  for (const r of records.slice(0, 50)) {
    for (const [k, v] of Object.entries(r)) {
      if (v === null || ["string", "number", "boolean"].includes(typeof v)) keys.add(k);
    }
  }
  return Array.from(keys);
}

function cell(v: unknown): string {
  if (v === null || v === undefined) return "";
  if (typeof v === "boolean") return v ? "✓" : "—";
  return String(v);
}

function ProbeView({ probe }: { probe: Probe }) {
  const records = probe.body != null ? recordsOf(probe.body) : null;
  const cols = records && records.length ? scalarColumns(records) : [];
  const raw =
    probe.body != null
      ? JSON.stringify(probe.body, null, 2)
      : (probe.bodyText ?? "");

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center justify-between gap-2 text-sm">
          <span className="flex items-center gap-2">
            {probe.error || !probe.ok ? (
              <AlertTriangle className="h-4 w-4 text-destructive" />
            ) : (
              <CheckCircle2 className="h-4 w-4 text-emerald-400" />
            )}
            {probe.name}
            <span
              className={
                "rounded px-1.5 py-0.5 font-mono text-[11px] " +
                (probe.ok
                  ? "bg-emerald-500/15 text-emerald-400"
                  : "bg-destructive/15 text-destructive")
              }
            >
              {probe.status || "ERR"}
            </span>
          </span>
          {records && <span className="text-xs text-muted-foreground">{records.length} records</span>}
        </CardTitle>
        <code className="block truncate text-[11px] text-muted-foreground">{probe.endpoint}</code>
      </CardHeader>
      <CardContent className="space-y-3">
        {probe.error && <p className="text-sm text-destructive">{probe.error}</p>}

        {records && records.length > 0 && (
          <div className="overflow-x-auto rounded-md border border-border">
            <table className="w-full text-xs">
              <thead className="bg-muted/40">
                <tr>
                  {cols.map((c) => (
                    <th key={c} className="whitespace-nowrap px-2 py-1 text-left font-mono font-medium">
                      {c}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {records.slice(0, 50).map((r, i) => (
                  <tr key={i} className="border-t border-border/60">
                    {cols.map((c) => (
                      <td key={c} className="max-w-[240px] truncate px-2 py-1" title={cell(r[c])}>
                        {cell(r[c])}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {raw && (
          <details className="group">
            <summary className="cursor-pointer text-xs text-primary hover:underline">
              Raw response
            </summary>
            <pre className="mt-2 max-h-96 overflow-auto rounded-md bg-muted/30 p-3 text-[11px] leading-relaxed">
              {raw.length > 60000 ? raw.slice(0, 60000) + "\n…(truncated)" : raw}
            </pre>
          </details>
        )}
      </CardContent>
    </Card>
  );
}

function SourceDiagnosticsPanel() {
  const [source, setSource] = useState<string>("audible");
  const run = useMutation<DiagResult, Error, string>({
    mutationFn: (s) => api.getBooksDiagnostics({ source: s }),
  });
  const result = run.data;
  const banner = useMemo(() => (run.error ? run.error.message : null), [run.error]);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex items-center gap-2">
          <BookOpen className="h-5 w-5 text-primary" />
          <h2 className="text-lg font-semibold">Books · source diagnostics</h2>
        </div>
        <div className="ml-auto flex items-center gap-2">
          <div className="flex rounded-md border border-border p-0.5">
            {SOURCES.map((s) => (
              <button
                key={s.id}
                type="button"
                onClick={() => setSource(s.id)}
                className={
                  "rounded px-3 py-1 text-sm " +
                  (source === s.id ? "bg-primary text-primary-foreground" : "text-muted-foreground")
                }
              >
                {s.label}
              </button>
            ))}
          </div>
          <Button size="sm" disabled={run.isPending} onClick={() => run.mutate(source)}>
            {run.isPending ? (
              <Loader2 className="mr-2 h-4 w-4 animate-spin" />
            ) : (
              <Play className="mr-2 h-4 w-4" />
            )}
            Run diagnostics
          </Button>
        </div>
      </div>

      <p className="text-sm text-muted-foreground">
        Fires raw signed requests at the {source} endpoints using your connected Amazon credential
        and dumps everything they return — for deciding which metrics to track. Connect Amazon in
        Settings first.
      </p>

      {banner && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {banner}
        </div>
      )}

      {result && (
        <div className="space-y-4">
          <div className="text-xs text-muted-foreground">
            source: <span className="font-mono">{result.source}</span> · marketplace:{" "}
            <span className="font-mono">{result.marketplace}</span>
          </div>
          {result.probes.map((p) => (
            <ProbeView key={p.name} probe={p} />
          ))}
        </div>
      )}
    </div>
  );
}

// rm2 · Raw feed — the human-readable RAW heartbeat/position stream from BOTH
// reading sources, complementing the Grafana cadence board with an in-app view.
// A tight table per source: Kindle furthest-page-read samples (asin/title/
// location/Δloc/creationTime/interval) + Audible per-day listening roll-ups.
function EmptyStream({ label }: { label: string }) {
  return (
    <div className="rounded-md border border-dashed border-border px-3 py-6 text-center text-xs text-muted-foreground">
      No recent {label} samples yet — turn the monitor on (or start Diagnostic
      Mode) and read to populate this feed.
    </div>
  );
}

function RawFeedPanel() {
  const { data, isLoading, isError, refetch, isFetching } = useQuery({
    queryKey: qk.readingMonitorRaw(),
    queryFn: () => api.getReadingMonitorRaw(),
  });

  const kindle = data?.kindle ?? [];
  const audible = data?.audible ?? [];

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-3">
        <div className="flex items-center gap-2">
          <Radar className="h-5 w-5 text-primary" />
          <h2 className="text-lg font-semibold">Books · raw reading feed</h2>
        </div>
        <Button
          size="sm"
          variant="outline"
          className="ml-auto"
          disabled={isFetching}
          onClick={() => refetch()}
        >
          <RefreshCw className={"mr-2 h-4 w-4 " + (isFetching ? "animate-spin" : "")} />
          Refresh
        </Button>
      </div>

      <p className="text-sm text-muted-foreground">
        The most-recent RAW samples the monitor observed from both reading
        sources — the human-readable complement to the Grafana cadence board.
        Kindle rows are furthest-page-read observations (Δloc is the position
        jump since the prior sample); Audible rows are per-day listening
        roll-ups.
      </p>

      {isError && (
        <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          Couldn&apos;t load the raw feed. Try Refresh.
        </div>
      )}

      {/* Kindle stream */}
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="flex items-center gap-2 text-sm">
            <Tablet className="h-4 w-4 text-primary" />
            Kindle · position stream
            <span className="ml-auto text-xs text-muted-foreground">
              {kindle.length} samples
            </span>
          </CardTitle>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="flex items-center gap-2 py-4 text-sm text-muted-foreground">
              <Loader2 className="h-4 w-4 animate-spin" /> Loading…
            </div>
          ) : kindle.length === 0 ? (
            <EmptyStream label="Kindle" />
          ) : (
            <div className="overflow-x-auto rounded-md border border-border">
              <table className="w-full text-xs">
                <thead className="bg-muted/40">
                  <tr>
                    {["asin", "title", "location", "Δloc", "creationTime", "interval"].map(
                      (c) => (
                        <th
                          key={c}
                          className="whitespace-nowrap px-2 py-1 text-left font-mono font-medium"
                        >
                          {c}
                        </th>
                      ),
                    )}
                  </tr>
                </thead>
                <tbody>
                  {kindle.map((k, i) => (
                    <tr key={`${k.asin}-${i}`} className="border-t border-border/60">
                      <td className="px-2 py-1 font-mono">{k.asin}</td>
                      <td className="max-w-[260px] truncate px-2 py-1" title={k.title}>
                        {k.title}
                      </td>
                      <td className="px-2 py-1 font-mono tabular-nums">{k.location}</td>
                      <td className="px-2 py-1 font-mono tabular-nums text-primary">
                        {k.dloc >= 0 ? `+${k.dloc}` : k.dloc}
                      </td>
                      <td
                        className="whitespace-nowrap px-2 py-1"
                        title={k.creationTime}
                      >
                        {k.creationTime ? relativeTime(k.creationTime) : "—"}
                      </td>
                      <td className="px-2 py-1 font-mono tabular-nums">
                        {`~${k.intervalSecs}s`}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>

      {/* Audible stream */}
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="flex items-center gap-2 text-sm">
            <Headphones className="h-4 w-4 text-primary" />
            Audible · listening stream
            <span className="ml-auto text-xs text-muted-foreground">
              {audible.length} samples
            </span>
          </CardTitle>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <div className="flex items-center gap-2 py-4 text-sm text-muted-foreground">
              <Loader2 className="h-4 w-4 animate-spin" /> Loading…
            </div>
          ) : audible.length === 0 ? (
            <EmptyStream label="Audible" />
          ) : (
            <div className="overflow-x-auto rounded-md border border-border">
              <table className="w-full text-xs">
                <thead className="bg-muted/40">
                  <tr>
                    {["title", "day", "listening-seconds"].map((c) => (
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
                  {audible.map((a, i) => (
                    <tr key={`${a.title}-${a.day}-${i}`} className="border-t border-border/60">
                      <td className="max-w-[320px] truncate px-2 py-1" title={a.title}>
                        {a.title}
                      </td>
                      <td className="whitespace-nowrap px-2 py-1 font-mono">{a.day}</td>
                      <td className="px-2 py-1 font-mono tabular-nums">
                        {a.listeningSeconds}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

// The Books admin tab hosts three diagnostics: the one-shot source DUMP
// (SourceDiagnosticsPanel), the LIVE reading monitor + Diagnostic Mode control
// (ReadingMonitorPanel), and the RAW reading feed (RawFeedPanel). A small
// segmented control switches between them — same admin section, no extra route.
const VIEWS = [
  { id: "diagnostics", label: "Source diagnostics", icon: BookOpen },
  { id: "monitor", label: "Reading monitor", icon: Radio },
  { id: "raw", label: "Raw feed", icon: Radar },
] as const;

type BooksView = (typeof VIEWS)[number]["id"];

function isBooksView(v: string | null): v is BooksView {
  return VIEWS.some((view) => view.id === v);
}

export function BooksTab() {
  // rm2 · honor ?view=monitor|raw|diagnostics so the global nav indicator can
  // deep-link straight to the reading-monitor tab. Defaults to diagnostics.
  const [params, setParams] = useSearchParams();
  const initial = params.get("view");
  const [view, setViewState] = useState<BooksView>(
    isBooksView(initial) ? initial : "diagnostics",
  );

  function setView(next: BooksView) {
    setViewState(next);
    // Reflect the active view in the URL so the tab is shareable / stable
    // across a reload, without stacking history entries.
    const p = new URLSearchParams(params);
    p.set("view", next);
    setParams(p, { replace: true });
  }

  return (
    <AdminTabShell bodyClassName="space-y-4">
      <div className="flex w-fit rounded-md border border-border p-0.5">
        {VIEWS.map((v) => (
          <button
            key={v.id}
            type="button"
            onClick={() => setView(v.id)}
            className={
              "flex items-center gap-1.5 rounded px-3 py-1 text-sm " +
              (view === v.id
                ? "bg-primary text-primary-foreground"
                : "text-muted-foreground")
            }
          >
            <v.icon className="h-4 w-4" />
            {v.label}
          </button>
        ))}
      </div>
      {view === "diagnostics" && <SourceDiagnosticsPanel />}
      {view === "monitor" && <ReadingMonitorPanel />}
      {view === "raw" && <RawFeedPanel />}
    </AdminTabShell>
  );
}
