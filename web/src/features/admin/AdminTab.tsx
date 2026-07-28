// AdminTab.tsx — Settings > Admin (gaka-364.3).
//
// v3 shape: the labels catalog itself is now editable from this tab. Each
// row shows the current thumbnail + label + kind + status (has image y/n)
// + generated_at + actions. Clicking a row opens a right-side Sheet with
// a full editor:
//   - label / glyph / description / optimizedPrompt / rank / tier / kind
//   - condition (raw JSONB textarea — MVP; tree editor is a follow-up)
//   - per-request generation overrides: model / size / seed
//   - Save / Save + regen / Cancel
//
// Also in the tab:
//   - Global generation config (systemPrompt textarea + save)
//   - Download seed.sql button (dumps the current DB state as a fresh
//     migration body — an operator can commit it back to git)
//   - The existing "regenerate all missing / regenerate all" bulk actions
//
// The old v2 behavior (parallel per-label regens, MAX_PARALLEL_REGENS
// throttling, per-row spinners) is preserved — a full 114-label regen
// still fires N per-label POSTs each with its own 600s server window.
//
// Only rendered when the current user is on the BOOM_ADMIN_USERS allowlist
// (Settings.tsx filters the tab out otherwise; the server also 403s any
// non-admin request, so the tab is a UX aid, not a security boundary).
import { Fragment, useEffect, useMemo, useRef, useState } from "react";
import type { ComponentPropsWithoutRef, ReactNode } from "react";
import { cn } from "@/lib/utils";
import {
  RefreshCw,
  ImageOff,
  CheckCircle2,
  Loader2,
  Zap,
  Download,
  Pencil,
  Save,
  X,
  Trash2,
  AlertTriangle,
  Wifi,
  WifiOff,
} from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Label as UILabel } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";
import { Textarea } from "@thebranchdriftcatalyst/catalyst-ui/ui/textarea";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/sheet";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { useLabelsCatalog } from "@/features/publicprofile/labels/useLabelsCatalog";
import type { LabelCatalogRow } from "@/features/publicprofile/labels/types";
import { LabelImage } from "@/features/publicprofile/labels/LabelImage";
import { useImageJobQueue, type JobState } from "./useImageJobQueue";

// --- ResizableSheetContent --------------------------------------------------
//
// Radix Sheet gives us a fixed-width panel; wrap it with a boomtime-local
// helper that adds a drag handle on the LEFT edge (matches side="right").
// Grab the handle, drag left/right, the sheet grows/shrinks in real time.
// Width is persisted per storageKey to localStorage so an operator's
// preferred size for the label editor survives page reloads.
//
// Not touching catalyst-ui — this stays local to boomtime.
//
// Test note: automated drag tests are avoided (jsdom mousemove synthesis is
// flaky). Manual verification only; the storage read/write is
// deterministic + testable via the width prop.

interface ResizableSheetContentProps
  extends ComponentPropsWithoutRef<typeof SheetContent> {
  minWidth?: number;
  maxWidth?: number;
  defaultWidth?: number;
  /** localStorage key to persist the drag-set width across reloads. */
  storageKey?: string;
  children?: ReactNode;
}

function ResizableSheetContent({
  children,
  minWidth = 400,
  maxWidth = 1200,
  defaultWidth = 560,
  storageKey = "admin-label-sheet-width",
  className,
  style,
  ...sheetProps
}: ResizableSheetContentProps) {
  const [width, setWidth] = useState<number>(() => {
    if (typeof window === "undefined") return defaultWidth;
    const saved = Number(window.localStorage.getItem(storageKey));
    return Number.isFinite(saved) && saved >= minWidth && saved <= maxWidth
      ? saved
      : defaultWidth;
  });
  const dragging = useRef(false);

  useEffect(() => {
    if (typeof window === "undefined") return;
    try {
      window.localStorage.setItem(storageKey, String(width));
    } catch {
      /* localStorage may be disabled (private browsing); silently degrade */
    }
  }, [width, storageKey]);

  useEffect(() => {
    function onMove(e: MouseEvent) {
      if (!dragging.current) return;
      // Sheet slides in from the right; the drag handle is on the LEFT
      // edge of the sheet, so width = viewport.right - clientX.
      const next = Math.min(
        maxWidth,
        Math.max(minWidth, window.innerWidth - e.clientX),
      );
      setWidth(next);
    }
    function onUp() {
      if (!dragging.current) return;
      dragging.current = false;
      document.body.style.userSelect = "";
      document.body.style.cursor = "";
    }
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
    return () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
  }, [minWidth, maxWidth]);

  return (
    <SheetContent
      {...sheetProps}
      // Drop padding on the outer container so the drag handle sits flush
      // at the left edge. The inner scroll div re-adds it.
      className={cn("p-0", className)}
      style={{ width, maxWidth: "none", ...style }}
    >
      {/* Drag handle — 6px vertical strip. Transparent by default, tints
          on hover so it's discoverable without shouting. */}
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize sheet"
        onMouseDown={(e) => {
          e.preventDefault();
          dragging.current = true;
          document.body.style.userSelect = "none";
          document.body.style.cursor = "col-resize";
        }}
        className="absolute left-0 top-0 z-10 h-full w-1.5 cursor-col-resize bg-transparent transition-colors hover:bg-[color:var(--primary)]/40"
        data-testid="sheet-resize-handle"
      />
      {/* Inner scroll container so the handle stays static as the form
          scrolls. Padding restored here. */}
      <div className="h-full overflow-y-auto p-6">{children}</div>
    </SheetContent>
  );
}

// --- helpers ----------------------------------------------------------------

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

// Trigger a browser download of `text` as `filename`. Used by the seed.sql
// dump button. Everything happens client-side; no server-side redirect.
function downloadText(filename: string, text: string) {
  const blob = new Blob([text], { type: "text/plain;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

// --- row status badge -------------------------------------------------------
//
// One place to render the per-row status pill so the row-render loop
// stays readable. Job status wins over the DB-derived "present/missing"
// so a fresh regen shows "queued"/"generating…" immediately even if the
// old image is still in place.

interface RowStatusBadgeProps {
  hasPrompt: boolean;
  hasImage: boolean;
  job: JobState | undefined;
}

function RowStatusBadge({ hasPrompt, hasImage, job }: RowStatusBadgeProps) {
  if (!hasPrompt) {
    return <span className="text-muted-foreground">no prompt</span>;
  }
  if (job) {
    switch (job.status) {
      case "queued":
        return (
          <span className="inline-flex items-center gap-1 rounded-sm border border-border bg-muted/40 px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider text-muted-foreground">
            <span>queued</span>
          </span>
        );
      case "running": {
        const elapsed = job.startedAt
          ? Math.max(0, Math.floor((Date.now() - new Date(job.startedAt).getTime()) / 1000))
          : 0;
        return (
          <span className="inline-flex items-center gap-1 text-primary">
            <Loader2 size={12} className="animate-spin" />
            <span>generating… {elapsed}s</span>
          </span>
        );
      }
      case "done":
        return (
          <span className="inline-flex items-center gap-1 text-emerald-500">
            <CheckCircle2 size={12} />
            <span>done</span>
          </span>
        );
      case "error":
        return (
          <span
            className="inline-flex items-center gap-1 text-destructive"
            title={job.error || "generation error"}
          >
            <AlertTriangle size={12} />
            <span>error</span>
          </span>
        );
    }
  }
  return hasImage ? (
    <span className="inline-flex items-center gap-1 text-emerald-500">
      <CheckCircle2 size={12} />
      <span>present</span>
    </span>
  ) : (
    <span className="inline-flex items-center gap-1 text-muted-foreground">
      <ImageOff size={12} />
      <span>missing</span>
    </span>
  );
}

// --- component --------------------------------------------------------------

export function AdminTab() {
  const queryClient = useQueryClient();

  // Two independent fetches: catalog (labels + systemPrompt) drives the
  // rows; adminLabelImages drives the per-row IMAGE metadata (bytes /
  // generated-at). Post gaka-8bz the aggressive 10s poll is dropped in
  // favor of a WS-driven queue view (useImageJobQueue). We keep this
  // query at a lazier 60s cadence as a safety net for cases where the
  // WS never fires an event (e.g. the operator opens the tab AFTER a
  // regen completed and was retention-expired out of the registry).
  const catalog = useLabelsCatalog();
  const status = useQuery({
    queryKey: qk.adminLabelImages(),
    refetchInterval: 60_000,
    queryFn: () => api.getAdminLabelImages(),
    staleTime: 30_000,
  });

  // gaka-8bz: durable server-side job queue + WS. Replaces the previous
  // client-side inFlight Set + MAX_PARALLEL_REGENS pool. The hook owns
  // the WS + reconnect; the server pool caps concurrency.
  const queue = useImageJobQueue();

  const [filter, setFilter] = useState<"all" | "missing" | "present">("all");
  const [selected, setSelected] = useState<LabelCatalogRow | null>(null);
  const [sysPromptDraft, setSysPromptDraft] = useState<string>("");

  // Keep the systemPrompt draft in sync with the fetched value when we
  // don't have an outstanding edit. Compare against undefined so we
  // seed the draft ONCE from the fetch, then only refresh it on catalog
  // refetches if the user hasn't edited it in the meantime.
  useEffect(() => {
    if (catalog.systemPrompt !== undefined) {
      setSysPromptDraft(catalog.systemPrompt);
    }
  }, [catalog.systemPrompt]);

  const metaById = useMemo(() => {
    const m = new Map<string, { sizeBytes: number; generatedAt: string }>();
    for (const it of status.data?.items ?? []) m.set(it.id, it);
    return m;
  }, [status.data]);

  const rows = catalog.rows;
  const filteredRows = rows.filter((r) => {
    if (filter === "missing") return !metaById.has(r.id);
    if (filter === "present") return metaById.has(r.id);
    return true;
  });
  const missingIds = rows
    .filter((r) => r.optimizedPrompt && !metaById.has(r.id))
    .map((r) => r.id);

  // Grouped structure: KIND → (for tier only) axis → rows sorted by tier
  // ladder. Other kinds are a single flat sub-group sorted by rank desc.
  // Memoized against the filtered set so filter chips update grouping.
  const grouped = useMemo(() => {
    const byKind = new Map<string, LabelCatalogRow[]>();
    for (const r of filteredRows) {
      const list = byKind.get(r.kind) ?? [];
      list.push(r);
      byKind.set(r.kind, list);
    }
    return KIND_ORDER.filter((k) => byKind.has(k)).map((kind) => {
      const rowsOfKind = byKind.get(kind)!;
      if (kind !== "tier") {
        return {
          kind,
          subGroups: [
            {
              axis: "",
              rows: [...rowsOfKind].sort((a, b) => b.rank - a.rank),
            },
          ],
        };
      }
      // Tier: sub-group by axis, sort each axis by the tier ladder.
      const byAxis = new Map<string, LabelCatalogRow[]>();
      for (const r of rowsOfKind) {
        const axis = tierAxis(r.id);
        const list = byAxis.get(axis) ?? [];
        list.push(r);
        byAxis.set(axis, list);
      }
      const subGroups = Array.from(byAxis.entries())
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([axis, rs]) => ({
          axis,
          rows: rs.sort((a, b) => {
            const ta = TIER_ORDER.indexOf(a.tier || "");
            const tb = TIER_ORDER.indexOf(b.tier || "");
            if (ta !== tb) return ta - tb;
            return b.rank - a.rank;
          }),
        }));
      return { kind, subGroups };
    });
  }, [filteredRows]);

  // --- per-label regen ------------------------------------------------------
  // Post gaka-8bz: enqueue is fire-and-forget; the WS drives all UI state.
  // Wrapping in a small callable keeps a stable identity for the row
  // Regen button + the Sheet's Save-and-regen call site.
  const enqueueOne = async (params: {
    id: string;
    prompt: string;
    model?: string;
    size?: string;
    seed?: number;
  }) => {
    try {
      await queue.enqueue({
        labelId: params.id,
        prompt: params.prompt,
        model: params.model,
        size: params.size,
        seed: params.seed,
      });
    } catch {
      // The server 400/500 flows through as a thrown ApiError; the WS
      // will not emit anything for a rejected enqueue. Fire a lazy
      // adminLabelImages refetch so the metadata column still refreshes
      // if this was a client-side networking flake and the server DID
      // start work.
      queryClient.invalidateQueries({ queryKey: qk.adminLabelImages() });
    }
  };

  // Bulk regen — just fire enqueue() N times. The server queue absorbs
  // concurrency (BOOM_LABEL_IMAGE_CONCURRENCY, default 2); the FE no
  // longer runs a fake pool. bulkRunning stays as a UI flag only for
  // the "fire" phase — it flips off as soon as every enqueue has been
  // accepted, which is nearly instant since each POST just returns 202.
  const [bulkRunning, setBulkRunning] = useState(false);
  async function bulkRegenerate(idsToRun: string[]) {
    if (bulkRunning) return;
    setBulkRunning(true);
    try {
      const promptById = new Map<string, string>();
      for (const r of rows) promptById.set(r.id, r.optimizedPrompt);
      await Promise.all(
        idsToRun.map(async (id) => {
          const prompt = promptById.get(id);
          if (!prompt) return;
          await enqueueOne({ id, prompt });
        }),
      );
    } finally {
      setBulkRunning(false);
    }
  }

  // Count of active (queued + running) jobs across all labels — drives
  // the bulk button's "in flight" tally so the operator gets ambient
  // feedback while the server chews through the queue.
  const activeJobCount = useMemo(() => {
    let n = 0;
    for (const j of queue.jobs.values()) {
      if (j.status === "queued" || j.status === "running") n++;
    }
    return n;
  }, [queue.jobs]);

  // --- global systemPrompt save --------------------------------------------
  const saveSysPrompt = useMutation({
    mutationFn: (sp: string) => api.adminUpdateLabelGenConfig(sp),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: qk.labelsCatalog() });
    },
  });

  // --- seed.sql download ---------------------------------------------------
  const [downloading, setDownloading] = useState(false);
  async function handleSeedDownload() {
    if (downloading) return;
    setDownloading(true);
    try {
      const sql = await api.adminLabelsSeedSQL();
      const stamp = new Date().toISOString().slice(0, 19).replace(/[:T]/g, "");
      downloadText(`labels_seed_${stamp}.sql`, sql);
    } finally {
      setDownloading(false);
    }
  }

  function bustHintFor(id: string): string | number | undefined {
    const meta = metaById.get(id);
    if (!meta) return undefined;
    return new Date(meta.generatedAt).getTime();
  }

  if (catalog.isLoading || status.isLoading) {
    return <p className="text-sm text-muted-foreground">Loading admin status…</p>;
  }
  if (status.isError) {
    return (
      <div className="rounded-md border border-destructive/40 bg-destructive/10 p-4 text-sm">
        <p className="font-semibold">Admin access required.</p>
        <p className="mt-2 text-muted-foreground">
          {(status.error as Error)?.message ?? "You are not on the admin allowlist."}
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* --- LABELS + IMAGES ------------------------------------------------ */}
      <section className="rounded-md border border-border bg-card p-4">
        <div className="flex items-baseline justify-between">
          <h2 className="font-mono text-sm font-semibold uppercase tracking-wider">
            Labels catalog
          </h2>
          <span className="text-xs text-muted-foreground">
            {rows.length} labels · {status.data?.count ?? 0} images ·{" "}
            <strong>{status.data?.enabled ? "gen ON" : "gen OFF"}</strong>
          </span>
        </div>
        <p className="mt-1 text-xs text-muted-foreground">
          Edit label metadata + prompts live. Image generation uses the
          ComfyUI shim ({status.data?.shimUrl || "not configured"}) under
          model <code className="font-mono">{status.data?.model ?? "—"}</code>.
          Click a row to open the editor.
        </p>

        <div className="mt-4 flex flex-wrap items-center gap-2">
          <Button
            size="sm"
            variant="default"
            onClick={() => bulkRegenerate(missingIds)}
            disabled={!status.data?.enabled || bulkRunning || missingIds.length === 0}
            title="Enqueue regens for every catalog entry not currently in DB — server pool caps concurrency"
          >
            <RefreshCw size={14} className={bulkRunning ? "animate-spin" : ""} />
            <span className="ml-2">
              {activeJobCount > 0
                ? `Regen missing (${missingIds.length}) — ${activeJobCount} in flight`
                : `Regen missing (${missingIds.length})`}
            </span>
          </Button>
          <Button
            size="sm"
            variant="outline"
            onClick={() =>
              bulkRegenerate(rows.filter((r) => r.optimizedPrompt).map((r) => r.id))
            }
            disabled={!status.data?.enabled || bulkRunning}
            title="Enqueue regens for every catalog entry with a prompt"
          >
            Regen all ({rows.filter((r) => r.optimizedPrompt).length})
          </Button>
          {/* WS connection indicator — subtle badge, only visible when the
              hook is trying to reconnect. During normal operation the
              icon is a small connected pip so the operator can distinguish
              "queue is quiet" from "queue may not be updating live". */}
          <span
            className={cn(
              "inline-flex items-center gap-1 rounded-sm border px-1.5 py-0.5 font-mono text-[10px]",
              queue.connected
                ? "border-emerald-500/40 text-emerald-500"
                : "border-yellow-500/50 text-yellow-500",
            )}
            title={
              queue.connected
                ? "Live queue stream connected"
                : `Live queue stream reconnecting (attempt ${queue.reconnectAttempt})`
            }
          >
            {queue.connected ? <Wifi size={10} /> : <WifiOff size={10} />}
            <span>{queue.connected ? "live" : "reconnecting"}</span>
          </span>
          <Button
            size="sm"
            variant="outline"
            onClick={handleSeedDownload}
            disabled={downloading}
            title="Dump current DB catalog as a fresh goose migration file — commit back to git to snapshot operator edits"
          >
            <Download size={14} className={downloading ? "animate-pulse" : ""} />
            <span className="ml-2">
              {downloading ? "Preparing…" : "Download seed.sql"}
            </span>
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

        {!status.data?.enabled && (
          <p className="mt-4 rounded-sm border border-yellow-500/40 bg-yellow-500/10 p-3 text-xs">
            <strong>Image generation disabled.</strong> Set{" "}
            <code className="font-mono">BOOM_FEATURE_LABEL_IMAGES=on</code> AND{" "}
            <code className="font-mono">BOOM_COMFYUI_SHIM_URL=&lt;url&gt;</code>,
            then restart boomtime. Label CRUD still works without the
            generator.
          </p>
        )}

        <div className="mt-4 overflow-x-auto">
          <table className="w-full min-w-[720px] border-collapse text-xs">
            <thead>
              <tr className="border-b border-border text-left uppercase tracking-wide text-muted-foreground">
                <th className="py-2 pr-3 font-mono text-[10px]">Preview</th>
                <th className="py-2 pr-3 font-mono text-[10px]">Label</th>
                <th className="py-2 pr-3 font-mono text-[10px]">Kind</th>
                <th className="py-2 pr-3 font-mono text-[10px]">Rank</th>
                <th className="py-2 pr-3 font-mono text-[10px]">Status</th>
                <th className="py-2 pr-3 font-mono text-[10px]">Generated</th>
                <th className="py-2 pr-3 font-mono text-[10px]">Size</th>
                <th className="py-2 font-mono text-[10px]">Actions</th>
              </tr>
            </thead>
            <tbody>
              {filteredRows.length === 0 && (
                <tr>
                  <td colSpan={8} className="py-6 text-center text-muted-foreground">
                    No labels match this filter.
                  </td>
                </tr>
              )}
              {grouped.map(({ kind, subGroups }) => {
                const totalInKind = subGroups.reduce((s, sg) => s + sg.rows.length, 0);
                return (
                  <Fragment key={kind}>
                    {/* Kind header row — 4 sections total (MEME/TIERS/ARCHETYPES/TRIBES) */}
                    <tr className="border-y border-primary/40 bg-primary/10">
                      <td
                        colSpan={8}
                        className="py-2 px-3 font-mono text-[11px] uppercase tracking-[0.2em] text-primary"
                      >
                        <span className="mr-2 text-primary/70">▓</span>
                        {KIND_HEADERS[kind]}
                        <span className="ml-3 text-muted-foreground">· {totalInKind}</span>
                      </td>
                    </tr>
                    {subGroups.map(({ axis, rows: rowsInAxis }) => (
                      <Fragment key={axis || "_flat"}>
                        {/* Sub-header ONLY for TIER kind (axis grouping: PYTHON / VIM / MAC / ...) */}
                        {kind === "tier" && axis && (
                          <tr className="border-b border-border/40 bg-muted/20">
                            <td
                              colSpan={8}
                              className="py-1 pl-8 pr-3 font-mono text-[10px] uppercase tracking-[0.16em] text-amber-500/80"
                            >
                              {axis.toUpperCase()}
                              <span className="ml-2 text-muted-foreground/70">· {rowsInAxis.length}</span>
                            </td>
                          </tr>
                        )}
                        {rowsInAxis.map((r) => {
                          const meta = metaById.get(r.id);
                          const job = queue.byLabel(r.id);
                          const hasPrompt = !!r.optimizedPrompt;
                          return (
                            <tr
                              key={r.id}
                              className="cursor-pointer border-b border-border/50 hover:bg-primary/5"
                              onClick={() => setSelected(r)}
                            >
                              <td className="py-2 pr-3">
                                <div className="h-10 w-10">
                                  <LabelImage
                                    id={r.id}
                                    size={40}
                                    bustHint={bustHintFor(r.id)}
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
                                  {r.glyph ? <span className="mr-1">{r.glyph}</span> : null}
                                  {r.label}
                                </div>
                                <div className="text-[10px] text-muted-foreground">
                                  {r.id}
                                </div>
                              </td>
                              <td className="py-2 pr-3 font-mono text-[10px] uppercase tracking-wide">
                                {r.kind}
                                {r.kind === "tier" && r.tier ? ` · ${r.tier}` : ""}
                              </td>
                              <td className="py-2 pr-3 font-mono text-[10px] tabular-nums">
                                {r.rank}
                              </td>
                              <td className="py-2 pr-3">
                                <RowStatusBadge
                                  hasPrompt={hasPrompt}
                                  job={job}
                                  hasImage={!!meta}
                                />
                              </td>
                              <td className="py-2 pr-3 font-mono text-[10px] tabular-nums text-muted-foreground">
                                {meta ? fmtRelative(meta.generatedAt) : "—"}
                              </td>
                              <td className="py-2 pr-3 font-mono text-[10px] tabular-nums text-muted-foreground">
                                {meta ? fmtBytes(meta.sizeBytes) : "—"}
                              </td>
                              <td className="py-2" onClick={(e) => e.stopPropagation()}>
                                <div className="flex items-center gap-1">
                                  <Button
                                    size="sm"
                                    variant="ghost"
                                    onClick={() => setSelected(r)}
                                    title="Edit this label"
                                  >
                                    <Pencil size={12} />
                                  </Button>
                                  {hasPrompt && (
                                    <Button
                                      size="sm"
                                      variant={meta ? "outline" : "default"}
                                      onClick={() =>
                                        enqueueOne({ id: r.id, prompt: r.optimizedPrompt })
                                      }
                                      disabled={
                                        !status.data?.enabled ||
                                        job?.status === "queued" ||
                                        job?.status === "running"
                                      }
                                      title={
                                        job?.status === "queued"
                                          ? "Already queued on the server"
                                          : job?.status === "running"
                                          ? "Currently running on the server"
                                          : meta
                                          ? "Enqueue a regen for this label"
                                          : "Enqueue an initial generation for this label"
                                      }
                                    >
                                      {job?.status === "running" ? (
                                        <Loader2 size={12} className="animate-spin" />
                                      ) : meta ? (
                                        <RefreshCw size={12} />
                                      ) : (
                                        <Zap size={12} />
                                      )}
                                    </Button>
                                  )}
                                </div>
                              </td>
                            </tr>
                          );
                        })}
                      </Fragment>
                    ))}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        </div>
      </section>

      {/* --- GLOBAL GENERATION CONFIG --------------------------------------- */}
      <section className="rounded-md border border-border bg-card p-4">
        <h2 className="font-mono text-sm font-semibold uppercase tracking-wider">
          Global generation config
        </h2>
        <p className="mt-1 text-xs text-muted-foreground">
          Prepended to every per-label{" "}
          <code className="font-mono text-[10px]">optimizedPrompt</code>{" "}
          before hitting the shim (SDXL tag convention:{" "}
          <code className="font-mono">
            {"{systemPrompt}, {perLabelOptimizedPrompt}"}
          </code>
          ). Cached in the worker for 30s — an edit is visible on the
          next regen batch.
        </p>
        <div className="mt-4 space-y-2">
          <UILabel htmlFor="sys-prompt">System prompt</UILabel>
          <Textarea
            id="sys-prompt"
            value={sysPromptDraft}
            onChange={(e) => setSysPromptDraft(e.target.value)}
            rows={4}
            className="font-mono text-[11px]"
            placeholder="cyberpunk anime chibi half-body emblem portrait, ..."
          />
          <div className="flex items-center justify-between">
            <span className="text-[10px] text-muted-foreground">
              {sysPromptDraft.length} chars
            </span>
            <Button
              size="sm"
              onClick={() => saveSysPrompt.mutate(sysPromptDraft)}
              disabled={
                saveSysPrompt.isPending ||
                sysPromptDraft === catalog.systemPrompt
              }
            >
              {saveSysPrompt.isPending ? (
                <>
                  <Loader2 size={12} className="animate-spin" />
                  <span className="ml-1.5">Saving…</span>
                </>
              ) : (
                <>
                  <Save size={12} />
                  <span className="ml-1.5">Save systemPrompt</span>
                </>
              )}
            </Button>
          </div>
        </div>
      </section>

      {/* --- EDIT SHEET ----------------------------------------------------- */}
      <LabelEditSheet
        row={selected}
        onClose={() => setSelected(null)}
        onSaved={() => {
          queryClient.invalidateQueries({ queryKey: qk.labelsCatalog() });
        }}
        onRegen={(id, prompt, model, size, seed) => {
          void enqueueOne({ id, prompt, model, size, seed });
        }}
        canRegen={!!status.data?.enabled}
        generatedAt={selected ? metaById.get(selected.id)?.generatedAt : undefined}
      />
    </div>
  );
}

// --- Sheet editor -----------------------------------------------------------

interface LabelEditSheetProps {
  row: LabelCatalogRow | null;
  onClose: () => void;
  onSaved: () => void;
  onRegen: (
    id: string,
    prompt: string,
    model?: string,
    size?: string,
    seed?: number,
  ) => void;
  canRegen: boolean;
  /** last-generated timestamp for cache-busting the preview image after regen. */
  generatedAt?: string;
}

// Local editable-form state. Kept simple (untyped strings for numeric
// fields → parsed on submit) so the form stays responsive even with
// invalid intermediate input. Validation is fail-loud on save.
interface EditDraft {
  kind: string;
  label: string;
  glyph: string;
  description: string;
  optimizedPrompt: string;
  rank: string;
  tier: string;
  conditionJson: string;
  // Per-request overrides — not persisted on the label, threaded into
  // the regen request only.
  model: string;
  size: string;
  seed: string;
}

function toDraft(row: LabelCatalogRow): EditDraft {
  return {
    kind: row.kind,
    label: row.label,
    glyph: row.glyph,
    description: row.description,
    optimizedPrompt: row.optimizedPrompt,
    rank: String(row.rank),
    tier: row.tier,
    conditionJson: JSON.stringify(row.condition, null, 2),
    model: "",
    size: "1024x1024",
    seed: "",
  };
}

const KIND_OPTIONS = ["tier", "archetype", "tribe", "meme"] as const;
const TIER_OPTIONS = ["novice", "apprentice", "adept", "master", "legend"] as const;

// ---- taxonomy grouping (catalog table) ------------------------------------
// Presentation order: memecore first (highest signal on the profile), tier
// second (largest section), archetypes + tribes last. Matches LabelsShowcase
// so operator + viewer share a mental model.
const KIND_ORDER: Array<LabelCatalogRow["kind"]> = [
  "meme",
  "tier",
  "archetype",
  "tribe",
];
const KIND_HEADERS: Record<LabelCatalogRow["kind"], string> = {
  meme: "OP SHIZNIT",
  tier: "TIERS",
  archetype: "ARCHETYPES",
  tribe: "TRIBES",
};
// Prefixes on tier IDs identify the AXIS. Strip when rendering the axis
// header (e.g. `languages-python-master` → axis "python" under sub-header PYTHON).
const TIER_ID_PREFIXES = [
  "languages-",
  "editors-",
  "platforms-",
  "categories-",
  "projects-",
];
const TIER_ORDER = ["novice", "apprentice", "adept", "master", "legend"];

function tierAxis(id: string): string {
  let s = id;
  for (const p of TIER_ID_PREFIXES) {
    if (s.startsWith(p)) {
      s = s.slice(p.length);
      break;
    }
  }
  const idx = s.lastIndexOf("-");
  return idx > 0 ? s.slice(0, idx) : s;
}

function LabelEditSheet({ row, onClose, onSaved, onRegen, canRegen, generatedAt }: LabelEditSheetProps) {
  const queryClient = useQueryClient();
  const [draft, setDraft] = useState<EditDraft | null>(row ? toDraft(row) : null);
  const [conditionErr, setConditionErr] = useState<string | null>(null);
  const [deleteConfirm, setDeleteConfirm] = useState(false);

  // Re-seed the draft whenever a new row is selected. `row?.id` (not `row`)
  // is the dep so re-fetches of the SAME row don't trample in-flight edits.
  useEffect(() => {
    if (row) {
      setDraft(toDraft(row));
      setConditionErr(null);
      setDeleteConfirm(false);
    }
  }, [row?.id]);

  const saveMutation = useMutation({
    mutationFn: async (d: EditDraft) => {
      if (!row) return null;
      const parsed = parseCondition(d.conditionJson);
      if (parsed.error) throw new Error(parsed.error);
      return api.adminUpdateLabel(row.id, {
        kind: d.kind as LabelCatalogRow["kind"],
        label: d.label,
        glyph: d.glyph,
        description: d.description,
        optimizedPrompt: d.optimizedPrompt,
        rank: Number(d.rank) || 0,
        tier: d.tier,
        condition: parsed.value as LabelCatalogRow["condition"],
      });
    },
    onSuccess: () => {
      onSaved();
      onClose();
    },
    onError: (err) => {
      setConditionErr((err as Error).message);
    },
  });

  const deleteMutation = useMutation({
    mutationFn: async () => {
      if (!row) return;
      return api.adminDeleteLabel(row.id);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: qk.labelsCatalog() });
      queryClient.invalidateQueries({ queryKey: qk.adminLabelImages() });
      onClose();
    },
  });

  function saveAndRegen() {
    if (!row || !draft) return;
    const parsed = parseCondition(draft.conditionJson);
    if (parsed.error) {
      setConditionErr(parsed.error);
      return;
    }
    saveMutation.mutate(draft, {
      onSuccess: () => {
        // Fire regen with the just-saved prompt + per-request overrides.
        const seedNum = draft.seed ? Number(draft.seed) : undefined;
        onRegen(
          row.id,
          draft.optimizedPrompt,
          draft.model || undefined,
          draft.size || undefined,
          Number.isFinite(seedNum) ? (seedNum as number) : undefined,
        );
      },
    });
  }

  const open = row !== null;
  return (
    <Sheet open={open} onOpenChange={(v) => !v && onClose()}>
      <ResizableSheetContent
        side="right"
        storageKey="admin-label-editor-width"
        defaultWidth={560}
        minWidth={400}
        maxWidth={1200}
      >
        {row && draft && (
          <>
            <SheetHeader>
              <SheetTitle className="font-mono uppercase tracking-wider">
                {draft.glyph ? <span className="mr-2">{draft.glyph}</span> : null}
                {draft.label || row.label}
              </SheetTitle>
              <SheetDescription className="font-mono text-[10px]">
                {row.id}
              </SheetDescription>
            </SheetHeader>

            {/*
              Big preview — the label image at a size that actually reads.
              Aspect-square, capped at 400px so the sheet doesn't blow out
              when the operator drags it wide. bustHint = generatedAt so
              a fresh regen busts the immutable-1yr cache without a full
              page reload.
            */}
            <div className="mt-4 flex justify-center">
              <LabelImage
                id={row.id}
                size={400}
                bustHint={generatedAt}
                className="aspect-square w-full max-w-[400px] rounded-sm border border-[color:var(--primary)]/40 object-cover"
                fallback={
                  <div
                    aria-hidden
                    className="flex aspect-square w-full max-w-[400px] items-center justify-center rounded-sm border border-dashed border-[color:var(--primary)]/30 bg-[color:var(--muted)] text-8xl text-[color:var(--muted-foreground)]"
                  >
                    {draft.glyph || row.glyph || "·"}
                  </div>
                }
              />
            </div>

            <div className="mt-6 space-y-4">
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <UILabel htmlFor="ed-label">Label (display)</UILabel>
                  <Input
                    id="ed-label"
                    value={draft.label}
                    onChange={(e) =>
                      setDraft({ ...draft, label: e.target.value.toUpperCase() })
                    }
                    className="font-mono uppercase"
                  />
                </div>
                <div>
                  <UILabel htmlFor="ed-glyph">Glyph (1-3 chars)</UILabel>
                  <Input
                    id="ed-glyph"
                    value={draft.glyph}
                    onChange={(e) => setDraft({ ...draft, glyph: e.target.value })}
                    maxLength={8}
                    className="font-mono"
                  />
                </div>
              </div>

              <div>
                <UILabel htmlFor="ed-desc">Description (rich narrative)</UILabel>
                <Textarea
                  id="ed-desc"
                  value={draft.description}
                  onChange={(e) => setDraft({ ...draft, description: e.target.value })}
                  rows={3}
                  className="text-[12px]"
                />
              </div>

              <div>
                <UILabel htmlFor="ed-opt">Optimized prompt (sent to comfyui)</UILabel>
                <Textarea
                  id="ed-opt"
                  value={draft.optimizedPrompt}
                  onChange={(e) =>
                    setDraft({ ...draft, optimizedPrompt: e.target.value })
                  }
                  rows={3}
                  className="font-mono text-[11px]"
                  placeholder="tag-heavy SDXL prompt (systemPrompt prepends at gen time)"
                />
              </div>

              <div className="grid grid-cols-3 gap-3">
                <div>
                  <UILabel htmlFor="ed-rank">Rank</UILabel>
                  <Input
                    id="ed-rank"
                    type="number"
                    value={draft.rank}
                    onChange={(e) => setDraft({ ...draft, rank: e.target.value })}
                    className="font-mono tabular-nums"
                  />
                </div>
                <div>
                  <UILabel htmlFor="ed-kind">Kind</UILabel>
                  <select
                    id="ed-kind"
                    value={draft.kind}
                    onChange={(e) => setDraft({ ...draft, kind: e.target.value })}
                    className="mt-1 h-9 w-full rounded-md border border-input bg-background px-2 font-mono text-xs uppercase tracking-wide"
                    disabled
                    title="Kind is not editable here — it defines the condition schema. Delete + recreate to change."
                  >
                    {KIND_OPTIONS.map((k) => (
                      <option key={k} value={k}>
                        {k}
                      </option>
                    ))}
                  </select>
                </div>
                <div>
                  <UILabel htmlFor="ed-tier">Tier (kind=tier only)</UILabel>
                  <select
                    id="ed-tier"
                    value={draft.tier}
                    onChange={(e) => setDraft({ ...draft, tier: e.target.value })}
                    disabled={draft.kind !== "tier"}
                    className="mt-1 h-9 w-full rounded-md border border-input bg-background px-2 font-mono text-xs uppercase tracking-wide"
                  >
                    <option value="">—</option>
                    {TIER_OPTIONS.map((t) => (
                      <option key={t} value={t}>
                        {t}
                      </option>
                    ))}
                  </select>
                </div>
              </div>

              <div>
                <UILabel htmlFor="ed-cond">Condition (raw JSONB)</UILabel>
                <Textarea
                  id="ed-cond"
                  value={draft.conditionJson}
                  onChange={(e) => {
                    setDraft({ ...draft, conditionJson: e.target.value });
                    setConditionErr(null);
                  }}
                  rows={8}
                  className="font-mono text-[11px]"
                />
                {conditionErr && (
                  <p className="mt-1 text-[11px] text-destructive">
                    {conditionErr}
                  </p>
                )}
              </div>

              <div className="border-t border-border pt-4">
                <p className="mb-2 font-mono text-[10px] uppercase tracking-wide text-muted-foreground">
                  Per-request generation overrides (not persisted)
                </p>
                <div className="grid grid-cols-3 gap-3">
                  <div>
                    <UILabel htmlFor="ed-model">Model</UILabel>
                    <Input
                      id="ed-model"
                      value={draft.model}
                      onChange={(e) => setDraft({ ...draft, model: e.target.value })}
                      placeholder="(env default)"
                      className="font-mono text-xs"
                    />
                  </div>
                  <div>
                    <UILabel htmlFor="ed-size">Size</UILabel>
                    <Input
                      id="ed-size"
                      value={draft.size}
                      onChange={(e) => setDraft({ ...draft, size: e.target.value })}
                      placeholder="1024x1024"
                      className="font-mono text-xs"
                    />
                  </div>
                  <div>
                    <UILabel htmlFor="ed-seed">Seed</UILabel>
                    <Input
                      id="ed-seed"
                      type="number"
                      value={draft.seed}
                      onChange={(e) => setDraft({ ...draft, seed: e.target.value })}
                      placeholder="(random)"
                      className="font-mono text-xs tabular-nums"
                    />
                  </div>
                </div>
              </div>

              {/* Delete confirmation — two-click safety (delete button turns
                  into confirm after first click). */}
              <div className="border-t border-border pt-4">
                {deleteConfirm ? (
                  <div className="flex items-center gap-2 rounded-sm border border-destructive/60 bg-destructive/10 p-3">
                    <span className="text-xs">
                      Really delete <strong>{row.id}</strong>? This
                      cascades to the label_images row (if any).
                    </span>
                    <Button
                      size="sm"
                      variant="destructive"
                      onClick={() => deleteMutation.mutate()}
                      disabled={deleteMutation.isPending}
                    >
                      {deleteMutation.isPending ? (
                        <Loader2 size={12} className="animate-spin" />
                      ) : (
                        "Confirm delete"
                      )}
                    </Button>
                    <Button
                      size="sm"
                      variant="ghost"
                      onClick={() => setDeleteConfirm(false)}
                    >
                      Cancel
                    </Button>
                  </div>
                ) : (
                  <Button
                    size="sm"
                    variant="ghost"
                    className="text-destructive hover:bg-destructive/10"
                    onClick={() => setDeleteConfirm(true)}
                  >
                    <Trash2 size={12} />
                    <span className="ml-1.5">Delete label</span>
                  </Button>
                )}
              </div>
            </div>

            <SheetFooter className="mt-6">
              <Button variant="outline" onClick={onClose}>
                <X size={12} />
                <span className="ml-1.5">Cancel</span>
              </Button>
              <Button
                onClick={() => saveMutation.mutate(draft)}
                disabled={saveMutation.isPending}
              >
                {saveMutation.isPending ? (
                  <Loader2 size={12} className="animate-spin" />
                ) : (
                  <Save size={12} />
                )}
                <span className="ml-1.5">Save</span>
              </Button>
              <Button
                variant="default"
                onClick={saveAndRegen}
                disabled={saveMutation.isPending || !canRegen || !draft.optimizedPrompt}
                title={
                  !canRegen
                    ? "Image generation feature is disabled — set BOOM_FEATURE_LABEL_IMAGES=on"
                    : "Save + immediately regenerate this label's image"
                }
              >
                <Zap size={12} />
                <span className="ml-1.5">Save + regen</span>
              </Button>
            </SheetFooter>
          </>
        )}
      </ResizableSheetContent>
    </Sheet>
  );
}

// parseCondition guards the JSONB textarea. Returns {value, error} — error
// is a human-readable message the sheet renders under the field.
function parseCondition(txt: string): { value?: unknown; error?: string } {
  const trimmed = txt.trim();
  if (!trimmed) return { error: "condition JSON is required" };
  try {
    const parsed = JSON.parse(trimmed);
    if (parsed === null || typeof parsed !== "object") {
      return { error: "condition must be a JSON object" };
    }
    if (!("kind" in parsed)) {
      return { error: "condition must have a `kind` discriminant (e.g. axis-time, daily-avg, all, any, not)" };
    }
    return { value: parsed };
  } catch (e) {
    return { error: `invalid JSON: ${(e as Error).message}` };
  }
}
