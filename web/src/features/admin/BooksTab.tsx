// BooksTab — Admin › Books (gaka-books). A DIAGNOSTIC dump of everything the
// Audible / Kindle sources return for the admin's connected Amazon account, so
// we can inventory every available metric/field before committing the model.
// Each "probe" is a raw signed request; we show its status + the verbatim body
// (parsed JSON with a field table, or raw text for XML/error pages).
import { useMemo, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { AlertTriangle, BookOpen, CheckCircle2, Loader2, Play } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { api } from "@/lib/api";

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

export function BooksTab() {
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
