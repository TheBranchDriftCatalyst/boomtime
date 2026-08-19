// BookDetailSheet — a non-blocking side panel for one canonical Work: every
// provider edition (Audible / Kindle / Hardcover) that shares a Hardcover book id
// (or amazon_asin for unmatched siblings), in one view. Opened by clicking a row
// in the Books explorer. Reuses the shared cell components so status edits work
// from the panel too.
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import { Loader2, ExternalLink, Trash2 } from "lucide-react";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetDescription,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/sheet";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import {
  SourceBadge,
  StatusSelect,
  HardcoverBadge,
  ProgressBar,
  RatingEditor,
  ListChips,
} from "@books/features/books/cells";
import { formatMinutes } from "@books/features/books/booksExplorerConfig";
import { openHardcover } from "@books/features/books/hardcover";
import type { ReadingItemDTO } from "@/types/api";
import type { ReadEvent } from "@/types/meta";

export function BookDetailSheet({
  item,
  onOpenChange,
}: {
  item: ReadingItemDTO | null;
  onOpenChange: (open: boolean) => void;
}) {
  // Stable key for the Work: the Hardcover book id when matched, else the ASIN.
  const workKey = item
    ? item.hardcoverBookId != null
      ? `hc:${item.hardcoverBookId}`
      : `asin:${item.amazonAsin || item.externalId}`
    : "";

  const work = useQuery({
    queryKey: qk.bookWork(workKey),
    queryFn: () => api.getBookWork(item!),
    enabled: !!item,
    staleTime: 30_000,
  });

  // The clicked row always represents the Work's headline metadata; the editions
  // list may add its siblings. Fall back to just the clicked item while loading.
  const editions = work.data?.editions?.length ? work.data.editions : item ? [item] : [];
  const head = editions[0] ?? item;

  return (
    <Sheet open={!!item} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        // modal={false} → non-blocking: the rest of the page stays interactive.
        className="w-[26rem] overflow-y-auto sm:max-w-[26rem]"
        onInteractOutside={(e) => e.preventDefault()}
      >
        {head && (
          <>
            <SheetHeader className="space-y-2 pb-2">
              <div className="flex items-start gap-3">
                {head.coverUrl ? (
                  <img
                    src={head.coverUrl}
                    alt=""
                    className="h-24 w-16 shrink-0 rounded object-cover"
                  />
                ) : null}
                <div className="min-w-0">
                  <SheetTitle className="text-base leading-snug">
                    {head.title}
                  </SheetTitle>
                  <SheetDescription className="text-xs">
                    {head.authors}
                    {head.series ? ` · ${head.series}` : ""}
                  </SheetDescription>
                  {head.hardcoverBookId != null && (
                    <button
                      type="button"
                      onClick={() => openHardcover(head)}
                      className="mt-1 inline-flex items-center gap-1 text-xs text-fuchsia-500 hover:underline"
                    >
                      <ExternalLink className="h-3 w-3" /> View on Hardcover
                    </button>
                  )}
                </div>
              </div>
            </SheetHeader>

            {/* Hardcover lists — a property of the Work (all editions share them). */}
            {head.hardcoverLists && head.hardcoverLists.length > 0 && (
              <div className="flex flex-wrap items-center gap-1 py-1">
                <span className="mr-1 text-[11px] font-semibold uppercase tracking-widest text-muted-foreground">
                  Lists
                </span>
                <ListChips lists={head.hardcoverLists} max={20} />
              </div>
            )}

            {work.isLoading && (
              <div className="flex items-center gap-2 py-4 text-xs text-muted-foreground">
                <Loader2 className="h-3.5 w-3.5 animate-spin" /> Loading editions…
              </div>
            )}

            {/* One block per provider edition of this Work. */}
            <div className="space-y-3 py-2">
              <div className="text-[11px] font-semibold uppercase tracking-widest text-muted-foreground">
                {editions.length} edition{editions.length === 1 ? "" : "s"}
              </div>
              {editions.map((e) => (
                <div
                  key={`${e.source}:${e.externalId}`}
                  className="rounded-lg border border-border p-3"
                >
                  <div className="flex items-center justify-between gap-2">
                    <SourceBadge source={e.source} />
                    <StatusSelect item={e} />
                  </div>
                  <div className="mt-2 flex items-center gap-2">
                    <ProgressBar pct={e.progressPercent} />
                    <span className="text-xs text-muted-foreground">
                      {e.progressPercent}%
                    </span>
                  </div>
                  <dl className="mt-2 grid grid-cols-2 gap-x-3 gap-y-1 text-xs">
                    <Row label="Finished" value={fmtDate(e.finishedAt)} />
                    <Row
                      label="Runtime"
                      value={e.runtimeMin ? formatMinutes(e.runtimeMin) : "—"}
                    />
                    <Row label="Hardcover" value={<HardcoverBadge item={e} />} />
                    <Row label="Rating" value={<RatingEditor item={e} />} />
                  </dl>
                  {e.narrators ? (
                    <div className="mt-1 text-xs text-muted-foreground">
                      Narrated by {e.narrators}
                    </div>
                  ) : null}
                </div>
              ))}
            </div>

            {/* Read history — a book can be read more than once (migration 00078). */}
            {work.data?.reads && work.data.reads.length > 0 && (
              <ReadsHistory reads={work.data.reads} />
            )}
          </>
        )}
      </SheetContent>
    </Sheet>
  );
}

// ReadsHistory renders the per-read list with a delete affordance. Deleting removes
// the read locally AND (for Hardcover-origin reads) on Hardcover — handy for pruning
// the junk/empty reads Hardcover auto-creates when a status is set. Optimistic: the
// row disappears immediately and is restored on failure.
function ReadsHistory({ reads }: { reads: ReadEvent[] }) {
  const [removed, setRemoved] = useState<Set<number>>(new Set());
  const [deleting, setDeleting] = useState<number | null>(null);
  const visible = reads.filter((r) => !removed.has(r.id));
  if (visible.length === 0) return null;

  const del = async (r: ReadEvent) => {
    if (deleting != null) return;
    setDeleting(r.id);
    setRemoved((s) => new Set(s).add(r.id)); // optimistic
    try {
      const res = await api.deleteReadingEvent(r.id);
      toast.success(
        r.origin === "hardcover" && res.hardcoverDeleted
          ? "Read deleted (here + on Hardcover)"
          : "Read deleted",
      );
    } catch {
      setRemoved((s) => {
        const n = new Set(s);
        n.delete(r.id);
        return n;
      });
      toast.error("Couldn't delete read");
    } finally {
      setDeleting(null);
    }
  };

  return (
    <div className="space-y-2 border-t border-border pt-3">
      <div className="text-[11px] font-semibold uppercase tracking-widest text-muted-foreground">
        {visible.length} read{visible.length === 1 ? "" : "s"}
      </div>
      {visible.map((r) => (
        <div
          key={r.id}
          className="group flex items-center justify-between gap-2 rounded-md border border-border/60 px-2.5 py-1.5 text-xs"
        >
          <span className="text-muted-foreground">
            {r.startedAt ? fmtDate(r.startedAt) : "—"}
            {" → "}
            <span className="text-foreground">{fmtDate(r.finishedAt)}</span>
          </span>
          <div className="flex items-center gap-1.5">
            <span className="rounded-full bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
              {r.origin}
            </span>
            <button
              type="button"
              onClick={() => del(r)}
              disabled={deleting != null}
              title="Delete this read (also on Hardcover)"
              aria-label="Delete this read"
              className="rounded p-1 text-muted-foreground/60 opacity-0 transition-opacity hover:bg-destructive/10 hover:text-destructive focus-visible:opacity-100 group-hover:opacity-100 disabled:opacity-50"
            >
              {deleting === r.id ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <Trash2 className="h-3.5 w-3.5" />
              )}
            </button>
          </div>
        </div>
      ))}
    </div>
  );
}

function Row({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="text-right">{value ?? "—"}</dd>
    </>
  );
}

function fmtDate(iso?: string | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return isNaN(d.getTime())
    ? "—"
    : d.toLocaleDateString(undefined, {
        year: "numeric",
        month: "short",
        day: "numeric",
      });
}
