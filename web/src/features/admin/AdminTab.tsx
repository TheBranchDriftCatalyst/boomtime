// AdminTab.tsx — Settings > Admin (gaka-myv).
//
// Only rendered when the current user is on the BOOM_ADMIN_USERS allowlist
// (Settings.tsx filters the tab out otherwise; the server also 403s any
// non-admin request, so the tab is a UX aid, not a security boundary).
//
// v2 shape: per-label table. Each row shows the current thumbnail (or a
// glyph fallback), status (missing / present / regenerating), last
// generated_at + byte size when present, and an individual REGEN button
// that fires ONE POST with ids=[id]. That way each generation gets its
// own 600s server-side timeout window — no more single-batch-of-N tied
// to one HTTP request that dies before slow models (chroma-hd) finish.
// The "regenerate all" bulk buttons stay for convenience.
//
// Concurrency: FE fires per-label regens in parallel up to
// MAX_PARALLEL_REGENS at a time — the shim/ComfyUI serialize on GPU
// anyway, but staggered client-side requests avoid piling up ComfyUI's
// pending queue with orphans (each individual request has its own 600s
// budget so we're not fighting the timeout).
import { useEffect, useMemo, useState } from "react";
import { RefreshCw, ImageOff, CheckCircle2, Loader2, Zap } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { LABEL_CATALOG } from "@/features/publicprofile/labels/catalog";
import type { LabelSpec } from "@/features/publicprofile/labels/types";
import { LabelImage } from "@/features/publicprofile/labels/LabelImage";

const MAX_PARALLEL_REGENS = 2;

interface Entry {
  id: string;
  label: string;
  prompt: string;
  kind: LabelSpec["kind"];
}

// Extract the {id, prompt, label} pairs the backend needs from the FE catalog.
// Labels without an imagePrompt are skipped — no image to generate.
function catalogEntries(): Entry[] {
  const out: Entry[] = [];
  const seen = new Set<string>();
  for (const s of LABEL_CATALOG as LabelSpec[]) {
    if (!s.imagePrompt || seen.has(s.id)) continue;
    seen.add(s.id);
    out.push({ id: s.id, label: s.label, prompt: s.imagePrompt, kind: s.kind });
  }
  return out;
}

function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(2)} MB`;
}

function fmtRelative(iso: string): string {
  const then = new Date(iso).getTime();
  if (!Number.isFinite(then)) return "—";
  const diff = Date.now() - then;
  const s = Math.floor(diff / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
}

export function AdminTab() {
  const queryClient = useQueryClient();
  // Track per-id in-flight regens so buttons show per-row spinners and we
  // can gate MAX_PARALLEL_REGENS in "Regenerate missing"/"all" bulk paths.
  const [inFlight, setInFlight] = useState<Set<string>>(new Set());
  const [filter, setFilter] = useState<"all" | "missing" | "present">("all");

  const entries = useMemo(catalogEntries, []);
  const promptById = useMemo(() => {
    const m = new Map<string, string>();
    for (const e of entries) m.set(e.id, e.prompt);
    return m;
  }, [entries]);

  const { data, isLoading, isError, error } = useQuery({
    queryKey: qk.adminLabelImages(),
    // Poll every 10s so newly-saved rows show up as their generation
    // completes. Cheap query (single COUNT + light SELECT).
    refetchInterval: 10_000,
    queryFn: () => api.getAdminLabelImages(),
    staleTime: 5_000,
  });

  const metaById = useMemo(() => {
    const m = new Map<string, { sizeBytes: number; generatedAt: string }>();
    for (const it of data?.items ?? []) m.set(it.id, it);
    return m;
  }, [data]);

  const regenOne = useMutation({
    mutationFn: async (id: string) => {
      const prompt = promptById.get(id);
      if (!prompt) throw new Error(`no imagePrompt for ${id}`);
      // Single-id regen: the server-side goroutine gets its own 600s
      // window instead of sharing one with 67 other labels.
      return api.regenerateLabelImages({
        entries: [{ id, prompt }],
        ids: [id],
      });
    },
    onMutate: (id) => {
      setInFlight((prev) => new Set(prev).add(id));
    },
    onSettled: (_res, _err, id) => {
      setInFlight((prev) => {
        const next = new Set(prev);
        next.delete(id);
        return next;
      });
      // Bust the info query so the row's meta refreshes shortly.
      // The 10s poll will also catch it, but this feels snappier.
      setTimeout(() => {
        queryClient.invalidateQueries({ queryKey: qk.adminLabelImages() });
      }, 2_000);
    },
  });

  // Bulk: regenerate ALL missing images, throttled to MAX_PARALLEL_REGENS.
  // Not a single POST — fires N independent per-id POSTs so each has its
  // own 600s server timeout (that's the whole point of this v2 UI).
  const [bulkRunning, setBulkRunning] = useState(false);
  async function bulkRegenerate(idsToRun: string[]) {
    if (bulkRunning) return;
    setBulkRunning(true);
    try {
      // Simple pool: fire MAX_PARALLEL_REGENS at a time.
      let idx = 0;
      const workers = Array.from(
        { length: Math.min(MAX_PARALLEL_REGENS, idsToRun.length) },
        async () => {
          while (idx < idsToRun.length) {
            const my = idsToRun[idx++];
            try {
              await regenOne.mutateAsync(my);
            } catch {
              /* swallow — the row's in-flight state cleared in onSettled */
            }
          }
        },
      );
      await Promise.all(workers);
    } finally {
      setBulkRunning(false);
    }
  }

  // Force <LabelImage> to bust its cache after a regen — the immutable
  // Cache-Control means the browser would keep the old bytes otherwise.
  // The bust hint is the generatedAt epoch, so it changes on each new row.
  function bustHintFor(id: string): string | number | undefined {
    const meta = metaById.get(id);
    if (!meta) return undefined;
    return new Date(meta.generatedAt).getTime();
  }

  // Toast-y result banner for last bulk result; single-row results are shown
  // inline via the row state so we don't need a big banner for those.
  const [lastBulkAt, setLastBulkAt] = useState<number | null>(null);
  useEffect(() => {
    if (!bulkRunning && lastBulkAt !== null) return;
    if (!bulkRunning) return;
    setLastBulkAt(Date.now());
  }, [bulkRunning, lastBulkAt]);

  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading admin status…</p>;
  }
  if (isError) {
    return (
      <div className="rounded-md border border-destructive/40 bg-destructive/10 p-4 text-sm">
        <p className="font-semibold">Admin access required.</p>
        <p className="mt-2 text-muted-foreground">
          {(error as Error)?.message ?? "You are not on the admin allowlist."}
        </p>
      </div>
    );
  }

  const filtered = entries.filter((e) => {
    if (filter === "missing") return !metaById.has(e.id);
    if (filter === "present") return metaById.has(e.id);
    return true;
  });
  const missingIds = entries.filter((e) => !metaById.has(e.id)).map((e) => e.id);

  return (
    <div className="space-y-6">
      <section className="rounded-md border border-border bg-card p-4">
        <h2 className="font-mono text-sm font-semibold uppercase tracking-wider">
          Label images
        </h2>
        <p className="mt-1 text-xs text-muted-foreground">
          Generates emblem imagery for every label with an{" "}
          <code className="font-mono text-[10px]">imagePrompt</code> via the
          ComfyUI shim ({data?.shimUrl || "not configured"}). Feature is{" "}
          <strong>{data?.enabled ? "ON" : "OFF"}</strong>.
        </p>

        <dl className="mt-4 grid grid-cols-2 gap-3 text-xs sm:grid-cols-4">
          <div>
            <dt className="uppercase tracking-wide text-muted-foreground">Model</dt>
            <dd className="mt-1 font-mono">{data?.model ?? "—"}</dd>
          </div>
          <div>
            <dt className="uppercase tracking-wide text-muted-foreground">
              Rows in DB
            </dt>
            <dd className="mt-1 font-mono tabular-nums">{data?.count ?? 0}</dd>
          </div>
          <div>
            <dt className="uppercase tracking-wide text-muted-foreground">
              FE catalog (prompted)
            </dt>
            <dd className="mt-1 font-mono tabular-nums">{entries.length}</dd>
          </div>
          <div>
            <dt className="uppercase tracking-wide text-muted-foreground">
              In flight
            </dt>
            <dd className="mt-1 font-mono tabular-nums">{inFlight.size}</dd>
          </div>
        </dl>

        <div className="mt-6 flex flex-wrap items-center gap-2">
          <Button
            size="sm"
            variant="default"
            onClick={() => bulkRegenerate(missingIds)}
            disabled={
              !data?.enabled || bulkRunning || missingIds.length === 0
            }
            title="Fire per-label regens (max 2 in parallel) for every catalog entry not currently in DB"
          >
            <RefreshCw
              size={14}
              className={bulkRunning ? "animate-spin" : ""}
            />
            <span className="ml-2">
              {bulkRunning
                ? `Working — ${inFlight.size} in flight`
                : `Regen missing (${missingIds.length})`}
            </span>
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() => bulkRegenerate(entries.map((e) => e.id))}
            disabled={!data?.enabled || bulkRunning}
            title="Fire per-label regens for every catalog entry — DELETEs each row then generates fresh"
          >
            Regen all ({entries.length})
          </Button>
          <div className="ml-auto flex items-center gap-1 text-xs">
            <span className="text-muted-foreground">Filter:</span>
            {(["all", "missing", "present"] as const).map((f) => (
              <button
                key={f}
                type="button"
                onClick={() => setFilter(f)}
                className={
                  "rounded-sm border px-2 py-0.5 font-mono text-[10px] uppercase tracking-wide " +
                  (filter === f
                    ? "border-primary bg-primary/20 text-primary"
                    : "border-border text-muted-foreground hover:text-foreground")
                }
              >
                {f}
              </button>
            ))}
          </div>
        </div>

        {!data?.enabled && (
          <p className="mt-4 rounded-sm border border-yellow-500/40 bg-yellow-500/10 p-3 text-xs">
            <strong>Feature disabled.</strong> Set{" "}
            <code className="font-mono">BOOM_FEATURE_LABEL_IMAGES=on</code> AND{" "}
            <code className="font-mono">BOOM_COMFYUI_SHIM_URL=&lt;url&gt;</code>{" "}
            (and optionally{" "}
            <code className="font-mono">BOOM_COMFYUI_MODEL=&lt;pipeline&gt;</code>
            ), then restart boomtime.
          </p>
        )}

        <div className="mt-6 overflow-x-auto">
          <table className="w-full min-w-[600px] border-collapse text-xs">
            <thead>
              <tr className="border-b border-border text-left uppercase tracking-wide text-muted-foreground">
                <th className="py-2 pr-3 font-mono text-[10px]">Preview</th>
                <th className="py-2 pr-3 font-mono text-[10px]">Label</th>
                <th className="py-2 pr-3 font-mono text-[10px]">Status</th>
                <th className="py-2 pr-3 font-mono text-[10px]">Generated</th>
                <th className="py-2 pr-3 font-mono text-[10px]">Size</th>
                <th className="py-2 font-mono text-[10px]">Actions</th>
              </tr>
            </thead>
            <tbody>
              {filtered.length === 0 && (
                <tr>
                  <td colSpan={6} className="py-6 text-center text-muted-foreground">
                    No labels match this filter.
                  </td>
                </tr>
              )}
              {filtered.map((e) => {
                const meta = metaById.get(e.id);
                const running = inFlight.has(e.id);
                return (
                  <tr key={e.id} className="border-b border-border/50">
                    <td className="py-2 pr-3">
                      <div className="h-10 w-10">
                        <LabelImage
                          id={e.id}
                          size={40}
                          bustHint={bustHintFor(e.id)}
                          className="rounded-sm border border-border"
                          fallback={
                            <div className="flex h-10 w-10 items-center justify-center rounded-sm border border-dashed border-muted-foreground/40 text-muted-foreground">
                              <ImageOff size={14} />
                            </div>
                          }
                        />
                      </div>
                    </td>
                    <td className="py-2 pr-3">
                      <div className="font-mono text-[11px] uppercase tracking-wide text-foreground">
                        {e.label}
                      </div>
                      <div className="text-[10px] text-muted-foreground">
                        {e.id} · {e.kind}
                      </div>
                    </td>
                    <td className="py-2 pr-3">
                      {running ? (
                        <span className="inline-flex items-center gap-1 text-primary">
                          <Loader2 size={12} className="animate-spin" />
                          <span>generating…</span>
                        </span>
                      ) : meta ? (
                        <span className="inline-flex items-center gap-1 text-emerald-500">
                          <CheckCircle2 size={12} />
                          <span>present</span>
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1 text-muted-foreground">
                          <ImageOff size={12} />
                          <span>missing</span>
                        </span>
                      )}
                    </td>
                    <td className="py-2 pr-3 font-mono text-[10px] tabular-nums text-muted-foreground">
                      {meta ? fmtRelative(meta.generatedAt) : "—"}
                    </td>
                    <td className="py-2 pr-3 font-mono text-[10px] tabular-nums text-muted-foreground">
                      {meta ? fmtBytes(meta.sizeBytes) : "—"}
                    </td>
                    <td className="py-2">
                      <Button
                        size="sm"
                        variant={meta ? "outline" : "default"}
                        onClick={() => regenOne.mutate(e.id)}
                        disabled={!data?.enabled || running}
                        title={
                          meta
                            ? "Delete + regenerate this label's image (own 600s window)"
                            : "Generate this label's image (own 600s window)"
                        }
                      >
                        {running ? (
                          <>
                            <Loader2 size={12} className="animate-spin" />
                            <span className="ml-1.5">Working</span>
                          </>
                        ) : meta ? (
                          <>
                            <RefreshCw size={12} />
                            <span className="ml-1.5">Regen</span>
                          </>
                        ) : (
                          <>
                            <Zap size={12} />
                            <span className="ml-1.5">Generate</span>
                          </>
                        )}
                      </Button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}
