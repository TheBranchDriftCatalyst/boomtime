// LiberationPanel — the per-book liberation control in the detail sheet, and the
// library-wide sweep button (boom-w20s.16). The Libation rebuild's UI surface.
//
// AVAILABILITY. Every liberation route 404s when the feature is off, so rather
// than threading a flag through the app the components probe the status endpoint
// once and render nothing on failure. That keeps the books UI byte-identical for
// anyone who has not enabled liberation, with no config plumbing.
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Download, Loader2, RotateCw, Trash2, TriangleAlert } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { api } from "@shared/lib/api";

// Terminal + in-flight status vocabulary, mirroring the Go constants in
// internal/books/liberate/store.go.
const IN_FLIGHT = new Set(["licensing", "downloading", "converting"]);

const STATUS_LABEL: Record<string, string> = {
  pending: "Queued",
  licensing: "Requesting license…",
  downloading: "Downloading…",
  converting: "Converting…",
  liberated: "Liberated",
  failed: "Failed",
  denied: "Denied by Audible",
  unsupported_codec: "Unsupported codec",
  unsupported_format: "Unsupported format",
  skipped: "Skipped",
};

const STATUS_STYLE: Record<string, string> = {
  liberated: "bg-emerald-500/15 text-emerald-400 border-emerald-500/30",
  failed: "bg-destructive/15 text-destructive border-destructive/30",
  denied: "bg-destructive/15 text-destructive border-destructive/30",
  unsupported_codec: "bg-amber-500/15 text-amber-400 border-amber-500/30",
  unsupported_format: "bg-amber-500/15 text-amber-400 border-amber-500/30",
};

export function LiberationBadge({ status }: { status?: string | null }) {
  // 'none' is the server-side COALESCE for a book never attempted (it exists so
  // the group-by axis has a bucket). There is nothing to badge.
  if (!status || status === "none") return null;
  const style = STATUS_STYLE[status] ?? "bg-muted text-muted-foreground border-border";
  return (
    <span className={"rounded border px-1.5 py-0.5 font-mono text-[11px] " + style}>
      {STATUS_LABEL[status] ?? status}
    </span>
  );
}

/** useLiberationAvailable probes the status endpoint; false when the feature is off. */
export function useLiberationAvailable() {
  const q = useQuery({
    queryKey: ["liberation", "status"],
    queryFn: () => api.getLiberationStatus(),
    // The feature flag does not change at runtime, so this never needs refetching
    // on focus — and a 404 must not be retried into a stampede.
    staleTime: 5 * 60_000,
    retry: false,
  });
  return { available: !q.isError && !!q.data, status: q.data };
}

/** Per-book control, rendered inside the detail sheet. */
export function LiberationPanel({
  externalId,
  source,
  liberationStatus,
  liberationError,
  audioPath,
  audioBytes,
}: {
  externalId: string;
  source: string;
  liberationStatus?: string | null;
  liberationError?: string | null;
  audioPath?: string | null;
  audioBytes?: number | null;
}) {
  const { available } = useLiberationAvailable();
  const qc = useQueryClient();
  const [banner, setBanner] = useState<string | null>(null);

  const liberate = useMutation({
    mutationFn: (force: boolean) => api.liberateBook(externalId, force),
    onSuccess: (r) => {
      setBanner(`Queued (job ${r.jobId}). This runs in the background.`);
      void qc.invalidateQueries({ queryKey: ["liberation"] });
    },
    onError: (e: Error) => setBanner(e.message),
  });

  const forget = useMutation({
    mutationFn: (deleteFile: boolean) => api.forgetLiberation(externalId, deleteFile),
    onSuccess: (r) => {
      setBanner(r.fileDeleted ? "File deleted and state cleared." : "State cleared; file kept.");
      void qc.invalidateQueries({ queryKey: ["liberation"] });
    },
    onError: (e: Error) => setBanner(e.message),
  });

  // Liberation is Audible-only: a Kindle ebook has no audiobook to liberate.
  if (!available || source !== "audible") return null;

  const inFlight = IN_FLIGHT.has(liberationStatus ?? "");
  const done = liberationStatus === "liberated";
  // Denied is TERMINAL — Amazon refused the license, and retrying is how an
  // account gets flagged. Offer no button for it.
  const denied = liberationStatus === "denied";
  const busy = liberate.isPending || forget.isPending;

  return (
    <div className="space-y-2 rounded-lg border border-border bg-card/40 p-3">
      <div className="flex items-center justify-between gap-2">
        <span className="font-mono text-[11px] uppercase tracking-[0.2em] text-primary/80">
          Liberation
        </span>
        <LiberationBadge status={liberationStatus} />
      </div>

      {audioPath && (
        <p className="break-all font-mono text-[11px] text-muted-foreground">
          {audioPath}
          {audioBytes ? ` · ${fmtBytes(audioBytes)}` : ""}
        </p>
      )}

      {liberationError && (
        <p className="flex items-start gap-1.5 text-xs text-destructive">
          <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <span className="break-words">{liberationError}</span>
        </p>
      )}

      <div className="flex flex-wrap gap-2">
        <Button
          size="sm"
          variant={done ? "outline" : "default"}
          disabled={busy || inFlight || denied}
          title={denied ? "Audible refused a license for this title" : undefined}
          onClick={() => liberate.mutate(done)}
        >
          {busy ? (
            <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
          ) : done ? (
            <RotateCw className="mr-1.5 h-3.5 w-3.5" />
          ) : (
            <Download className="mr-1.5 h-3.5 w-3.5" />
          )}
          {inFlight ? "In progress…" : done ? "Re-liberate" : "Liberate"}
        </Button>

        {done && (
          <Button
            size="sm"
            variant="outline"
            disabled={busy}
            onClick={() => forget.mutate(true)}
            title="Delete the file from the library and clear its state"
          >
            <Trash2 className="mr-1.5 h-3.5 w-3.5" />
            Delete file
          </Button>
        )}
      </div>

      {banner && <p className="text-xs text-muted-foreground">{banner}</p>}
    </div>
  );
}

/** Library-wide sweep, rendered in the Books page toolbar. */
export function LiberateAllButton() {
  const { available, status } = useLiberationAvailable();
  const qc = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const [banner, setBanner] = useState<string | null>(null);

  const sweep = useMutation({
    mutationFn: () => api.sweepLiberation({}),
    onSuccess: (r) => {
      setConfirming(false);
      setBanner(`Queued ${r.pending} book${r.pending === 1 ? "" : "s"}.`);
      void qc.invalidateQueries({ queryKey: ["liberation"] });
    },
    onError: (e: Error) => {
      setConfirming(false);
      setBanner(e.message);
    },
  });

  if (!available) return null;
  const pending = status?.pending ?? 0;

  // The confirm is not ceremony. A first sweep of a real Audible library is
  // hundreds of gigabytes onto a NAS; the user should see the book count before
  // committing, not discover it from a disk-full alert.
  if (confirming) {
    return (
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <span className="text-muted-foreground">
          Liberate {pending} book{pending === 1 ? "" : "s"}? This downloads every one of
          them — expect roughly {estimateGB(pending)} on {status?.libraryPath || "the library volume"}.
        </span>
        <Button size="sm" disabled={sweep.isPending} onClick={() => sweep.mutate()}>
          {sweep.isPending && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
          Yes, liberate all
        </Button>
        <Button size="sm" variant="ghost" onClick={() => setConfirming(false)}>
          Cancel
        </Button>
      </div>
    );
  }

  return (
    <div className="flex items-center gap-2">
      <Button
        size="sm"
        variant="outline"
        disabled={pending === 0}
        onClick={() => setConfirming(true)}
        title={pending === 0 ? "Every book is already liberated" : undefined}
      >
        <Download className="mr-1.5 h-4 w-4" />
        Liberate all{pending > 0 ? ` (${pending})` : ""}
      </Button>
      {banner && <span className="text-xs text-muted-foreground">{banner}</span>}
    </div>
  );
}

/**
 * estimateGB gives a deliberately ROUGH size estimate. An average Audible title
 * at 128kbps runs ~400MB; the point is order-of-magnitude honesty before a user
 * commits a NAS to it, not accuracy we cannot have without licensing every book.
 */
/** fmtBytes renders a stored file size. */
function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(1)} ${units[i]}`;
}

function estimateGB(books: number): string {
  const gb = (books * 0.4).toFixed(books * 0.4 < 10 ? 1 : 0);
  return `${gb} GB`;
}
