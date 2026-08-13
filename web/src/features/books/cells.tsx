// cells.tsx — the shared Books cell renderers (gaka-02sh). Extracted verbatim
// from the old hand-rolled BooksTable so the groupable-explorer config
// (booksExplorerConfig.tsx) can reuse the exact same cover / badge / pill /
// progress / rating cells the flat table always rendered. No behavior change —
// only a move out of BooksPage.tsx so both the page chrome and the config draw
// the same components.
import { useState } from "react";
import { BookMarked, BookOpen, Headphones, Star } from "lucide-react";
import { cn } from "@/lib/utils";
import type { ReadingItemDTO } from "@/types/meta";

/** Format an ISO date to a compact "Aug 3, 2026" (em-dash for missing/invalid). */
export const fmtDate = (iso?: string): string => {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime())
    ? "—"
    : d.toLocaleDateString(undefined, {
        year: "numeric",
        month: "short",
        day: "numeric",
      });
};

// The source drives the badge glyph + palette. Audible = amber/headphones,
// Kindle = sky/book. Anything else falls back to a neutral chip.
export function SourceBadge({ source }: { source: string }) {
  const s = source.toLowerCase();
  if (s === "audible") {
    return (
      <span className="inline-flex items-center gap-1.5 rounded-full border border-amber-500/40 bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium text-amber-500 dark:text-amber-400">
        <Headphones className="h-3 w-3" />
        Audible
      </span>
    );
  }
  if (s === "kindle") {
    return (
      <span className="inline-flex items-center gap-1.5 rounded-full border border-sky-500/40 bg-sky-500/10 px-2 py-0.5 text-[11px] font-medium text-sky-600 dark:text-sky-400">
        <BookOpen className="h-3 w-3" />
        Kindle
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1.5 rounded-full border border-border bg-muted/40 px-2 py-0.5 text-[11px] font-medium text-muted-foreground">
      {source || "—"}
    </span>
  );
}

// Status pill: want / reading / read / paused / dnf. Each gets a distinct hue.
const STATUS_META: Record<string, { label: string; className: string }> = {
  reading: {
    label: "Reading",
    className: "border-primary/40 bg-primary/10 text-primary",
  },
  read: {
    label: "Finished",
    className:
      "border-emerald-500/40 bg-emerald-500/10 text-emerald-600 dark:text-emerald-400",
  },
  want: {
    label: "Want",
    className:
      "border-violet-500/40 bg-violet-500/10 text-violet-600 dark:text-violet-400",
  },
  paused: {
    label: "Paused",
    className:
      "border-amber-500/40 bg-amber-500/10 text-amber-600 dark:text-amber-400",
  },
  dnf: {
    label: "DNF",
    className:
      "border-rose-500/40 bg-rose-500/10 text-rose-600 dark:text-rose-400",
  },
};

export function StatusPill({
  status,
  finished,
}: {
  status: string;
  finished: boolean;
}) {
  const key = status.toLowerCase();
  const meta =
    STATUS_META[key] ?? (finished ? STATUS_META.read : STATUS_META.reading);
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full border px-2 py-0.5 text-[11px] font-medium",
        meta.className,
      )}
    >
      {meta.label}
    </span>
  );
}

// Hardcover match-state badge. Honest about the (current) unmatched reality:
// the link/match columns are NULL until the Hardcover match sync runs, so we
// render a muted "Not matched" today and auto-upgrade to "Synced"/status or a
// generic "Matched" the moment the DTO carries a hardcoverBookId.
export function HardcoverBadge({ item }: { item: ReadingItemDTO }) {
  const matched = item.hardcoverBookId != null;
  if (!matched) {
    return (
      <span
        className="inline-flex items-center rounded-full border border-border bg-muted/40 px-2 py-0.5 text-[11px] font-medium text-muted-foreground/70"
        title="Not yet matched to a Hardcover book"
      >
        Not matched
      </span>
    );
  }
  const status = (item.hardcoverStatus ?? "").trim();
  // A known shelf status → show it (capitalized); otherwise a generic "Matched".
  const label = status
    ? status.charAt(0).toUpperCase() + status.slice(1)
    : "Matched";
  return (
    <span
      className="inline-flex items-center gap-1 rounded-full border border-fuchsia-500/40 bg-fuchsia-500/10 px-2 py-0.5 text-[11px] font-medium text-fuchsia-600 dark:text-fuchsia-400"
      title={
        status
          ? `Matched on Hardcover · ${label}`
          : "Matched to a Hardcover book"
      }
    >
      <BookMarked className="h-3 w-3" />
      {status ? label : "Matched"}
    </span>
  );
}

// Slim progress bar — a neon fill over a track. Clamped 0..100.
export function ProgressBar({ pct }: { pct: number }) {
  const v = Math.max(0, Math.min(100, Math.round(pct)));
  return (
    <div className="flex items-center gap-2">
      <div className="h-1.5 w-20 overflow-hidden rounded-full bg-muted">
        <div
          className="h-full rounded-full bg-gradient-to-r from-primary/70 to-primary transition-all"
          style={{ width: `${v}%` }}
        />
      </div>
      <span className="w-8 shrink-0 text-right font-mono text-[11px] text-muted-foreground">
        {v}%
      </span>
    </div>
  );
}

// Rating cell: the user's own rating wins (accent star). Otherwise the Goodreads
// community average is shown muted with a "GR" hint so the two never blur.
export function RatingCell({ item }: { item: ReadingItemDTO }) {
  if (typeof item.rating === "number" && item.rating > 0) {
    return (
      <span className="inline-flex items-center gap-1 text-xs font-medium text-foreground">
        <Star className="h-3.5 w-3.5 fill-primary text-primary" />
        {item.rating.toFixed(1)}
      </span>
    );
  }
  if (typeof item.goodreadsRating === "number" && item.goodreadsRating > 0) {
    return (
      <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
        <Star className="h-3.5 w-3.5 text-muted-foreground/60" />
        {item.goodreadsRating.toFixed(2)}
        <span className="rounded bg-muted px-1 py-px font-mono text-[9px] uppercase tracking-wide text-muted-foreground/70">
          GR
        </span>
      </span>
    );
  }
  return <span className="text-muted-foreground/50">—</span>;
}

// Cover thumbnail with a graceful synthwave fallback tile when no URL exists
// (or the image 404s).
export function Cover({ item }: { item: ReadingItemDTO }) {
  const [broken, setBroken] = useState(false);
  const show = item.coverUrl && !broken;
  return (
    <div className="relative h-14 w-10 shrink-0 overflow-hidden rounded-sm ring-1 ring-border">
      {show ? (
        <img
          src={item.coverUrl}
          alt=""
          loading="lazy"
          onError={() => setBroken(true)}
          className="h-full w-full object-cover"
        />
      ) : (
        <div className="flex h-full w-full items-center justify-center bg-gradient-to-br from-primary/15 via-muted to-background">
          <BookMarked className="h-4 w-4 text-primary/50" />
        </div>
      )}
    </div>
  );
}

// Title / subtitle / series stack — the multi-line first data cell.
export function TitleCell({ item }: { item: ReadingItemDTO }) {
  return (
    <div className="max-w-[320px]">
      <div className="truncate font-medium text-foreground" title={item.title}>
        {item.title}
      </div>
      {item.subtitle && (
        <div
          className="truncate text-xs text-muted-foreground"
          title={item.subtitle}
        >
          {item.subtitle}
        </div>
      )}
      {item.series && (
        <div className="truncate text-[11px] italic text-primary/70">
          {item.series}
        </div>
      )}
    </div>
  );
}

// Author (+ narrator subline for audiobooks) cell.
export function AuthorCell({ item }: { item: ReadingItemDTO }) {
  const isAudio = item.source.toLowerCase() === "audible";
  return (
    <div className="max-w-[200px]">
      <div className="truncate text-sm" title={item.authors}>
        {item.authors || "—"}
      </div>
      {isAudio && item.narrators && (
        <div
          className="truncate text-[11px] text-muted-foreground"
          title={item.narrators}
        >
          Narrated by {item.narrators}
        </div>
      )}
    </div>
  );
}
