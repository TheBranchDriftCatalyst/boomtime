// LiberationPanel — the per-book liberation control in the detail sheet, and the
// library-wide sweep button (boom-w20s.16). The Libation rebuild's UI surface.
//
// AVAILABILITY. The liberation routes are only REGISTERED when the feature is
// on, so rather than threading a flag through the app the components probe the
// status endpoint once and render nothing when the probe does not come back
// shaped like a status. That keeps the books UI byte-identical for anyone who
// has not enabled liberation, with no config plumbing. See
// isLiberationStatus below for why "the request did not error" is NOT a usable
// availability signal on this server.
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, Download, Loader2, RotateCw, Trash2, TriangleAlert } from "lucide-react";
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

/** The GET /api/v1/books/liberation/status wire shape, pinned to the api client. */
type LiberationStatus = Awaited<ReturnType<typeof api.getLiberationStatus>>;

/**
 * isLiberationStatus — availability is proven by the response SHAPE, never by
 * "the request resolved".
 *
 * WHY THIS EXISTS. The Go SPA catch-all (internal/shared/server/server.go) claims
 * every unmatched GET and only 404s paths whose last segment contains a dot, so
 * with liberation OFF this extensionless path does not 404 — it answers
 * 200 text/html with the SPA index. The shared api client, in turn, falls back to
 * handing back the raw response TEXT when JSON.parse fails on an ok response. A
 * plain `!!q.data` availability test therefore reads a truthy HTML STRING as
 * "the feature is on" and renders the entire liberation surface on every
 * liberation-off install: a Liberate menu item and an enabled Liberate button
 * whose POST 405s against the catch-all, plus a "Liberate all" disabled with the
 * false tooltip "Every book is already liberated" (`(htmlString).pending` is
 * undefined → 0). web/e2e/books-liberation.spec.ts's own probe checks the shape
 * for exactly this reason; the hook has to hold the same line, independently of
 * whatever the api client decides to do with non-JSON bodies.
 */
function isLiberationStatus(v: unknown): v is LiberationStatus {
  if (typeof v !== "object" || v === null || Array.isArray(v)) return false;
  // `counts` may be an empty object on a library with no Audible titles, so its
  // presence — not its contents — is the marker; `pending` pins the type.
  return "counts" in v && typeof (v as { pending?: unknown }).pending === "number";
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
  // Anything that is not a status payload (the SPA index, an error envelope) is
  // treated as "feature off" — and is never handed on as `status`, so no consumer
  // can read `.pending`/`.excluded` off a non-status value.
  const status = isLiberationStatus(q.data) ? q.data : undefined;
  return { available: !q.isError && status !== undefined, status };
}

/**
 * Banner — a mutation outcome line, carrying whether it is a FAILURE.
 *
 * Before this the success and error branches of every liberation mutation set
 * the same plain string into the same muted line, so "Queued (job 42)" and
 * "liberation is not enabled" were typographically identical — a failed sweep
 * read as an informational status update. The tone travels with the message so
 * a failure cannot be rendered as neutral prose.
 */
type Banner = { text: string; error: boolean };

const ok = (text: string): Banner => ({ text, error: false });
const failed = (text: string): Banner => ({
  // An ApiError with no message (a bare network drop) must still say something.
  text: text || "Request failed.",
  error: true,
});

function BannerLine({
  banner,
  className,
  as = "p",
}: {
  banner: Banner;
  className?: string;
  as?: "p" | "span";
}) {
  const Tag = as;
  return (
    <Tag
      // role=alert so the failure is announced, not just coloured.
      role={banner.error ? "alert" : undefined}
      className={
        (className ? className + " " : "") +
        (banner.error ? "text-destructive" : "text-muted-foreground")
      }
    >
      {banner.text}
    </Tag>
  );
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
  const [banner, setBanner] = useState<Banner | null>(null);

  const liberate = useMutation({
    mutationFn: (force: boolean) => api.liberateBook(externalId, force),
    onSuccess: (r) => {
      setBanner(ok(`Queued (job ${r.jobId}). This runs in the background.`));
      void qc.invalidateQueries({ queryKey: ["liberation"] });
    },
    onError: (e: Error) => setBanner(failed(e.message)),
  });

  const forget = useMutation({
    mutationFn: (deleteFile: boolean) => api.forgetLiberation(externalId, deleteFile),
    onSuccess: (r) => {
      setBanner(ok(r.fileDeleted ? "File deleted and state cleared." : "State cleared; file kept."));
      void qc.invalidateQueries({ queryKey: ["liberation"] });
    },
    onError: (e: Error) => setBanner(failed(e.message)),
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

      {banner && <BannerLine banner={banner} className="text-xs" />}
    </div>
  );
}

/** Library-wide sweep, rendered in the Books page toolbar. */
export function LiberateAllButton() {
  const { available, status } = useLiberationAvailable();
  const qc = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const [banner, setBanner] = useState<Banner | null>(null);

  const sweep = useMutation({
    mutationFn: () => api.sweepLiberation({}),
    onSuccess: (r) => {
      setConfirming(false);
      setBanner(ok(`Queued ${r.pending} book${r.pending === 1 ? "" : "s"}.`));
      void qc.invalidateQueries({ queryKey: ["liberation"] });
    },
    onError: (e: Error) => {
      setConfirming(false);
      setBanner(failed(e.message));
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
      <SkippedList count={status?.excluded ?? 0} />
      {banner && <BannerLine banner={banner} className="text-xs" as="span" />}
    </div>
  );
}

/**
 * SkippedList — the give-up set: titles the sweep will no longer pick up, with
 * the reason it stopped.
 *
 * WHY THIS EXISTS. Excluding a hopeless title from the sweep is correct, but
 * before this the excluded rows were also INVISIBLE — a title Amazon refuses
 * every time looked exactly like one that had simply never been queued. Three
 * podcasts sat in that blind spot being re-requested indefinitely.
 *
 * The list is fetched only when opened. It is a small set by nature, and on a
 * healthy library it is empty, so paying for it on every Books page load would
 * be a request that almost always returns nothing.
 */
function SkippedList({ count }: { count: number }) {
  const [open, setOpen] = useState(false);
  const [retryBanner, setRetryBanner] = useState<Banner | null>(null);
  const qc = useQueryClient();

  const q = useQuery({
    queryKey: ["liberation", "excluded"],
    queryFn: () => api.getLiberationExcluded(),
    enabled: open,
  });

  // Retrying clears the row's liberation state, which also resets the
  // consecutive-failure counter — without that reset the title would run once
  // and be dropped by every sweep after, so the button would appear to work and
  // quietly not.
  const retry = useMutation({
    mutationFn: (asin: string) => api.forgetLiberation(asin, false),
    onMutate: () => setRetryBanner(null),
    onSuccess: () => {
      setRetryBanner(null);
      void qc.invalidateQueries({ queryKey: ["liberation"] });
    },
    // A silent failure here recreates the EXACT blind spot this list exists to
    // fix: without onError the row simply stays put — indistinguishable from a
    // successful retry whose invalidation has not landed yet — and the user
    // walks away believing a title Amazon keeps refusing is back in the sweep.
    onError: (e: Error) => setRetryBanner(failed(`Retry failed: ${e.message || "request failed"}`)),
  });

  if (count === 0) return null;

  return (
    <div className="text-xs">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-1 text-muted-foreground hover:text-foreground"
        aria-expanded={open}
      >
        {open ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronRight className="h-3.5 w-3.5" />}
        {count} skipped
      </button>

      {open && (
        <div className="mt-2 space-y-1.5 border-l border-border pl-3">
          {q.isPending && <p className="text-muted-foreground">Loading…</p>}
          {q.isError && <p className="text-destructive">Could not load the skipped list.</p>}
          {retryBanner && <BannerLine banner={retryBanner} />}
          {q.data?.items.map((it) => (
            <div key={it.asin} className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <p className="truncate font-medium">{it.title || it.asin}</p>
                <p className="text-muted-foreground">
                  {STATUS_LABEL[it.status] ?? it.status}
                  {/* Attempt count only means something for the give-up case; on
                      a terminal verdict it is always 1 and reads as noise. */}
                  {it.retryable && it.attempts > 0 && ` · ${it.attempts} attempts`}
                  {it.error && ` · ${it.error.slice(0, 120)}`}
                </p>
              </div>
              {it.retryable ? (
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={retry.isPending}
                  onClick={() => retry.mutate(it.asin)}
                >
                  <RotateCw className="mr-1 h-3 w-3" />
                  Retry
                </Button>
              ) : (
                // No button on a terminal verdict: pressing it would re-earn the
                // identical refusal from Amazon. Saying why is more useful than
                // offering an action that cannot work.
                <span className="shrink-0 pt-1 text-muted-foreground">won’t retry</span>
              )}
            </div>
          ))}
          {q.data?.items.length === 0 && (
            <p className="text-muted-foreground">Nothing skipped.</p>
          )}
        </div>
      )}
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
