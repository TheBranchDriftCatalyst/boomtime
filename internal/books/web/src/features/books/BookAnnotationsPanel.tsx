// BookAnnotationsPanel — one book's highlights, notes and clips in the detail
// sheet (boom-siwi.5).
//
// TWO THINGS THIS COMPONENT IS CAREFUL ABOUT:
//
// 1. FEATURE DETECTION. The endpoint is gated on BOOM_FEATURE_BOOKS_ANNOTATIONS,
//    and the Go SPA catch-all answers an unregistered extensionless GET with
//    HTTP 200 and the index HTML. `!!data` therefore reads a truthy HTML string
//    as "the feature is on" — the boom-28jm trap, sighted three separate times.
//    doRequest now rejects an HTML body, but LiberationPanel deliberately keeps a
//    second, independent line of defence and so does this: the payload is
//    SHAPE-CHECKED before it is trusted, and the panel renders nothing at all
//    when the shape is wrong.
//
// 2. PROVENANCE. `body` (what Amazon says the user highlighted) and `transcript`
//    (what a speech model made of a clip we cut ourselves) are rendered
//    distinguishably, with the transcript labelled by the model that produced it.
//    Presenting a machine guess as the user's own words would be the single most
//    misleading thing this panel could do.
import { useQuery } from "@tanstack/react-query";
import { Highlighter } from "lucide-react";

import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import type { BookAnnotationDTO } from "@shared/types/meta";

interface AnnotationsPayload {
  annotations: BookAnnotationDTO[];
  counts: Record<string, number>;
}

// isAnnotationsPayload is the shape guard. It checks a STRUCTURAL property the
// SPA fallback page cannot accidentally satisfy — an `annotations` array — rather
// than mere truthiness.
function isAnnotationsPayload(v: unknown): v is AnnotationsPayload {
  if (typeof v !== "object" || v === null || Array.isArray(v)) return false;
  return Array.isArray((v as { annotations?: unknown }).annotations);
}

export function useBookAnnotations(asin: string | undefined) {
  const q = useQuery({
    queryKey: qk.bookAnnotations(asin ?? ""),
    queryFn: () => api.getBookAnnotations(asin!),
    enabled: !!asin,
    staleTime: 60_000,
    // No retry: a 404 from a flag-off server is a settled answer, not a blip.
    retry: false,
  });
  const data = isAnnotationsPayload(q.data) ? q.data : undefined;
  return { available: !q.isError && data !== undefined, data };
}

/** formatPosition renders an offset in the units the ROW declares. */
function formatPosition(a: BookAnnotationDTO): string {
  switch (a.positionUnit) {
    case "millis":
      return a.positionEnd != null
        ? `${clock(a.positionStart)}–${clock(a.positionEnd)}`
        : clock(a.positionStart);
    case "page":
      return `Page ${a.positionStart.toLocaleString()}`;
    default:
      return `Location ${a.positionStart.toLocaleString()}`;
  }
}

function clock(ms: number): string {
  const s = Math.floor(ms / 1000);
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  const mm = `${m}`.padStart(h > 0 ? 2 : 1, "0");
  return `${h > 0 ? `${h}:` : ""}${mm}:${`${sec}`.padStart(2, "0")}`;
}

export function BookAnnotationsPanel({ asin }: { asin: string | undefined }) {
  const { available, data } = useBookAnnotations(asin);

  // Flag off, endpoint absent, or a shape we do not recognise: render nothing.
  // A book with genuinely no annotations also renders nothing — an empty
  // "0 highlights" block is noise on the majority of books.
  if (!available || !data || data.annotations.length === 0) return null;

  const total = data.annotations.length;
  return (
    <div className="space-y-2 border-t border-border pt-3">
      <div className="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-widest text-muted-foreground">
        <Highlighter className="h-3 w-3" />
        {total} annotation{total === 1 ? "" : "s"}
        {Object.entries(data.counts)
          .filter(([, n]) => n > 0)
          .map(([kind, n]) => (
            <span
              key={kind}
              className="rounded-full bg-muted px-1.5 py-0.5 text-[10px] font-normal normal-case tracking-normal text-muted-foreground"
            >
              {n} {kind}
              {n === 1 ? "" : "s"}
            </span>
          ))}
      </div>

      <div className="max-h-72 space-y-1.5 overflow-y-auto pr-1">
        {data.annotations.map((a) => (
          <div
            key={`${a.kind}-${a.positionStart}-${a.positionEnd ?? ""}`}
            className="rounded-md border border-border/60 px-2.5 py-1.5 text-xs"
          >
            <div className="flex items-center justify-between gap-2 text-[10px] text-muted-foreground">
              <span>{formatPosition(a)}</span>
              <span className="rounded-full bg-muted px-1.5 py-0.5">{a.kind}</span>
            </div>

            {a.body && <p className="mt-1 text-foreground">{a.body}</p>}

            {a.note && (
              <p className="mt-1 border-l-2 border-border pl-2 italic text-muted-foreground">
                {a.note}
              </p>
            )}

            {/* Machine-derived, and labelled as such — never presented as the
                user's own words. */}
            {a.transcript && (
              <p className="mt-1 border-l-2 border-primary/40 pl-2 text-muted-foreground">
                {a.transcript}
                {a.transcriptSource && (
                  <span className="ml-1 text-[10px] opacity-60">
                    (transcribed by {a.transcriptSource})
                  </span>
                )}
              </p>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
