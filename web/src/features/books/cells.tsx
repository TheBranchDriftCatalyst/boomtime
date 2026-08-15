// cells.tsx — the shared Books cell renderers (gaka-02sh). Extracted verbatim
// from the old hand-rolled BooksTable so the groupable-explorer config
// (booksExplorerConfig.tsx) can reuse the exact same cover / badge / pill /
// progress / rating cells the flat table always rendered. No behavior change —
// only a move out of BooksPage.tsx so both the page chrome and the config draw
// the same components.
import { useState } from "react";
import { toast } from "sonner";
import {
  ArrowRight,
  BookMarked,
  Bookmark,
  BookOpen,
  CalendarDays,
  Check,
  ChevronDown,
  Headphones,
  Star,
  X,
} from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dropdown-menu";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/popover";
import { Calendar } from "@/components/ui/calendar";
import { cn } from "@/lib/utils";
import { useSetBookCuration } from "@/features/books/useBookCuration";
import { BOOK_STATUSES, type BookStatus } from "@/types/meta";
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
// Kindle = sky/book, Hardcover = fuchsia/bookmark (the shared Hardcover accent).
// Anything else falls back to a neutral chip.
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
  if (s === "hardcover") {
    return (
      <span className="inline-flex items-center gap-1.5 rounded-full border border-fuchsia-500/40 bg-fuchsia-500/10 px-2 py-0.5 text-[11px] font-medium text-fuchsia-600 dark:text-fuchsia-400">
        <Bookmark className="h-3 w-3" />
        Hardcover
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
export const STATUS_META: Record<string, { label: string; className: string }> = {
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

/** Resolve the pill meta for a status key, with the legacy finished fallback. */
function statusMeta(status: string, finished: boolean) {
  const key = status.toLowerCase();
  return STATUS_META[key] ?? (finished ? STATUS_META.read : STATUS_META.reading);
}

export function StatusPill({
  status,
  finished,
}: {
  status: string;
  finished: boolean;
}) {
  const meta = statusMeta(status, finished);
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

// --- Curation provenance ------------------------------------------------------
//
// Three provenance states for a book's effective status (gaka-books):
//   auto      — plain Amazon-derived; the device layer, no override. No dot.
//   curated   — a user override you set (or just set optimistically). A filled
//               dot in the pill's own hue (currentColor) — "you curated this".
//   hardcover — an override boomtime ADOPTED from Hardcover (LWW pull). A
//               distinct fuchsia dot, matching the HardcoverBadge accent.
type Provenance = "auto" | "curated" | "hardcover";

/**
 * Classify the effective status. `pendingOverride` is the cell-local optimistic
 * value (set the instant a user picks a status, before the DTO round-trips) —
 * when present it always reads as `curated` (the user's own fresh edit).
 *
 * A subtlety the backend forces: the Amazon-finish promotion stamps
 * status_override='read' on EVERY natural finish (to advance the LWW clock), so
 * statusIsOverride is true for all finished books. We must NOT light the dot
 * there — that override doesn't DIVERGE from what Amazon derived (both 'read').
 * The dot only means "the effective status differs from the raw device layer":
 * override != derived. Within that, an override matching the last-seen Hardcover
 * shelf reads as adopted-from-Hardcover; otherwise it's a local curation.
 */
export function statusProvenance(
  item: ReadingItemDTO,
  pendingOverride?: string | null,
): Provenance {
  if (pendingOverride != null) return "curated";
  if (!item.statusIsOverride) return "auto";
  const override = (item.statusOverride ?? "").toLowerCase();
  const derived = (item.statusDerived ?? "").toLowerCase();
  // A natural finish (override == derived) is not a real curation — no dot.
  if (override && override === derived) return "auto";
  const hc = (item.hardcoverStatus ?? "").toLowerCase();
  const eff = (item.status ?? "").toLowerCase();
  return hc && hc === eff ? "hardcover" : "curated";
}

const PROV_TITLE: Record<Provenance, string> = {
  auto: "Amazon-derived status",
  curated: "Curated override — pushed to Hardcover",
  hardcover: "Adopted from Hardcover",
};

/** The subtle dot appended inside the status trigger. `auto` renders nothing. */
function ProvenanceDot({ provenance }: { provenance: Provenance }) {
  if (provenance === "auto") return null;
  return (
    <span
      title={PROV_TITLE[provenance]}
      aria-label={PROV_TITLE[provenance]}
      data-provenance={provenance}
      className={cn(
        "h-1.5 w-1.5 shrink-0 rounded-full ring-1",
        provenance === "hardcover"
          ? "bg-fuchsia-500 ring-fuchsia-500/40"
          : "bg-current ring-current/40 opacity-80",
      )}
    />
  );
}

// --- StatusSelect: the editable status pill -----------------------------------
//
// Replaces the read-only StatusPill in editable contexts. The trigger keeps the
// pill aesthetic (STATUS_META hue) + a caret + the provenance dot; the menu
// lists the 5 canonical statuses as mini-pills. Selecting one fires a curation
// PATCH via useSetBookCuration, optimistically flipping the pill immediately and
// rolling back (with a toast) on error.
export function StatusSelect({ item }: { item: ReadingItemDTO }) {
  const mut = useSetBookCuration(item);
  // Cell-local optimistic override: the picked status shows instantly and
  // survives a successful write (the explorer table isn't react-query backed,
  // so the row prop won't refresh until a reload — we hold the value here).
  const [override, setOverride] = useState<BookStatus | null>(null);

  const effective = (override ?? item.status ?? "reading").toLowerCase();
  const meta = statusMeta(effective, item.finished);
  const provenance = statusProvenance(item, override);

  function choose(next: BookStatus) {
    if (next === effective) return;
    const prev = override;
    setOverride(next); // optimistic
    mut.mutate(
      { status: next },
      {
        onError: () => {
          setOverride(prev); // rollback
          toast.error("Couldn't update status");
        },
        onSuccess: (dto) => {
          // Trust the server's effective status (a real Amazon finish can
          // promote past the pick), falling back to what we sent.
          setOverride(((dto.status as BookStatus) ?? next) || next);
        },
      },
    );
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          disabled={mut.isPending}
          aria-label={`Status: ${meta.label}. Change status`}
          className={cn(
            "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium transition-opacity",
            "hover:opacity-90 focus:outline-none focus-visible:ring-2 focus-visible:ring-ring",
            mut.isPending && "opacity-60",
            meta.className,
          )}
        >
          {meta.label}
          <ProvenanceDot provenance={provenance} />
          <ChevronDown className="h-3 w-3 opacity-60" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="min-w-36 p-1">
        {BOOK_STATUSES.map((s) => {
          const m = STATUS_META[s];
          const active = s === effective;
          return (
            <DropdownMenuItem
              key={s}
              onSelect={() => choose(s)}
              className="gap-2 px-1.5 py-1"
            >
              <span
                className={cn(
                  "inline-flex flex-1 items-center rounded-full border px-2 py-0.5 text-[11px] font-medium",
                  m.className,
                )}
              >
                {m.label}
              </span>
              <Check
                className={cn(
                  "h-3.5 w-3.5 shrink-0",
                  active ? "opacity-100" : "opacity-0",
                )}
              />
            </DropdownMenuItem>
          );
        })}
      </DropdownMenuContent>
    </DropdownMenu>
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
  const remote = (item.hardcoverStatus ?? "").trim().toLowerCase(); // last-seen Hardcover shelf
  const effective = (item.status ?? "").trim().toLowerCase(); // what boomtime would push
  const cap = (s: string) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : "");
  const remoteLabel = remote ? STATUS_META[remote]?.label ?? cap(remote) : "";
  const effLabel = effective ? STATUS_META[effective]?.label ?? cap(effective) : "";

  // SYNC DIFF: status is 1:1 with Hardcover, so when the effective (curated) status
  // diverges from the last-seen Hardcover shelf, the next Hardcover push WOULD change
  // the shelf remote→effective. Surface that pending change inline (dry-run-safe — it's
  // just what a sync would do). remote unknown = never pulled → treat as no-diff yet.
  const diff = remote && effective && remote !== effective;
  if (diff) {
    return (
      <span
        className="inline-flex items-center gap-1 rounded-full border border-amber-500/40 bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium text-amber-600 dark:text-amber-400"
        title={`Sync would update Hardcover: ${remoteLabel} → ${effLabel}. (Writes stay dry-run-gated until enabled.)`}
      >
        <BookMarked className="h-3 w-3" />
        {remoteLabel} <ArrowRight className="h-2.5 w-2.5" /> {effLabel}
      </span>
    );
  }

  // In sync (or remote status unknown): show the shelf status, else a generic "Matched".
  const label = remote ? remoteLabel : "Matched";
  return (
    <span
      className="inline-flex items-center gap-1 rounded-full border border-fuchsia-500/40 bg-fuchsia-500/10 px-2 py-0.5 text-[11px] font-medium text-fuchsia-600 dark:text-fuchsia-400"
      title={
        remote
          ? `Matched on Hardcover · ${label} (in sync)`
          : "Matched to a Hardcover book"
      }
    >
      <BookMarked className="h-3 w-3" />
      {label}
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

// --- RatingEditor: inline 1..5 star editor ------------------------------------
//
// Editable counterpart to RatingCell. Shows 5 stars for the effective rating
// (user override wins); clicking a star sets that rating, clicking the current
// rating again clears the override (falls back to the derived/GR layer). A tiny
// fuchsia dot marks a rating that came from Hardcover. Optimistic + rollback,
// same shape as StatusSelect.
export function RatingEditor({ item }: { item: ReadingItemDTO }) {
  const mut = useSetBookCuration(item);
  // null = untouched (use item's effective rating); a number = optimistic set.
  const [override, setOverride] = useState<number | null>(null);
  const touched = override !== null;
  const effective = touched ? override : (item.rating ?? 0);
  const rounded = Math.round(effective);
  const fromHardcover =
    !touched &&
    item.ratingOverride != null &&
    (item.hardcoverStatus ?? "") !== "";

  function choose(next: number) {
    const prev = override;
    const value = next === rounded ? null : next; // click-again clears
    setOverride(value ?? 0); // optimistic (0 renders as empty stars)
    mut.mutate(
      { rating: value },
      {
        onError: () => {
          setOverride(prev);
          toast.error("Couldn't update rating");
        },
        onSuccess: (dto) => setOverride(dto.rating ?? 0),
      },
    );
  }

  const hasUserRating = effective > 0;
  return (
    <div
      className="inline-flex items-center gap-0.5"
      role="radiogroup"
      aria-label="Rating"
    >
      {[1, 2, 3, 4, 5].map((n) => (
        <button
          key={n}
          type="button"
          role="radio"
          aria-checked={n === rounded}
          aria-label={`${n} star${n > 1 ? "s" : ""}`}
          disabled={mut.isPending}
          onClick={() => choose(n)}
          className="rounded p-0.5 text-muted-foreground/40 transition-colors hover:text-primary focus:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-60"
        >
          <Star
            className={cn(
              "h-3.5 w-3.5",
              n <= rounded && hasUserRating && "fill-primary text-primary",
            )}
          />
        </button>
      ))}
      {fromHardcover && (
        <span
          title="Rating adopted from Hardcover"
          aria-label="Rating adopted from Hardcover"
          className="ml-0.5 h-1.5 w-1.5 rounded-full bg-fuchsia-500 ring-1 ring-fuchsia-500/40"
        />
      )}
      {/* Goodreads community average as a muted hint when there's no user rating. */}
      {!hasUserRating &&
        typeof item.goodreadsRating === "number" &&
        item.goodreadsRating > 0 && (
          <span className="ml-1 rounded bg-muted px-1 py-px font-mono text-[9px] uppercase tracking-wide text-muted-foreground/70">
            GR {item.goodreadsRating.toFixed(1)}
          </span>
        )}
    </div>
  );
}

// --- FinishedEditor: inline finished-date editor ------------------------------
//
// Editable counterpart to the plain fmtDate cell. Shows the effective finished
// date as a button; clicking opens a calendar popover. Picking a day sets the
// finished_at override (RFC3339); Clear reverts to the derived layer. Optimistic
// + rollback.
export function FinishedEditor({ item }: { item: ReadingItemDTO }) {
  const mut = useSetBookCuration(item);
  const [open, setOpen] = useState(false);
  // undefined = untouched; a value (string | null) = optimistic override.
  const [override, setOverride] = useState<string | null | undefined>(
    undefined,
  );
  const effective = override !== undefined ? override : (item.finishedAt ?? null);
  const selected = effective ? new Date(effective) : undefined;

  function commit(next: string | null) {
    const prev = override;
    setOverride(next); // optimistic
    setOpen(false);
    mut.mutate(
      { finishedAt: next },
      {
        onError: () => {
          setOverride(prev);
          toast.error("Couldn't update finished date");
        },
        onSuccess: (dto) => setOverride(dto.finishedAt ?? null),
      },
    );
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          disabled={mut.isPending}
          aria-label={
            effective ? `Finished ${fmtDate(effective)}. Change` : "Set finished date"
          }
          className={cn(
            "inline-flex items-center gap-1 whitespace-nowrap rounded px-1 py-0.5 text-xs transition-colors hover:bg-muted focus:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-60",
            effective ? "text-muted-foreground" : "text-muted-foreground/50",
          )}
        >
          <CalendarDays className="h-3 w-3 opacity-70" />
          {effective ? fmtDate(effective) : "—"}
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-auto p-2">
        <Calendar
          mode="single"
          selected={selected}
          defaultMonth={selected}
          onSelect={(d?: Date) => d && commit(d.toISOString())}
        />
        {effective && (
          <button
            type="button"
            onClick={() => commit(null)}
            className="mt-1 flex w-full items-center justify-center gap-1 rounded-sm px-2 py-1 text-xs text-muted-foreground hover:bg-muted"
          >
            <X className="h-3 w-3" />
            Clear finished date
          </button>
        )}
      </PopoverContent>
    </Popover>
  );
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
