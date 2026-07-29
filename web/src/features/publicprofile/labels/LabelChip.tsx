// LabelChip — universal chip renderer for an awarded label.
//
// One component so every surface (hero tagline, labels-showcase widget,
// future admin previews) gets the same tooltip contract: hovering the chip
// pops a rich card with the label's image, description (what earns it),
// and reason (the specific numbers that triggered it for THIS user).
//
// Uses catalyst-ui's Tooltip primitive (Radix under the hood). Assumes
// <TooltipProvider> is mounted at the app root (see main.tsx).
//
// gaka-mem-chip: added atop the memeification framework (gaka-364) and the
// label-images pipeline (gaka-myv) — pulls both together into one visual
// primitive so downstream widgets don't reimplement chip chrome each time.
// We import Radix Tooltip primitives DIRECTLY (bypassing catalyst-ui's
// re-export) so the Root / Trigger / Portal / Content all share the same
// bundled context. catalyst-ui bundles its own copy of @radix-ui/react-tooltip
// via a rollup chunk, so a Portal imported separately would fail with
// "TooltipPortal must be used within Tooltip" — the contexts don't line up
// across bundles. The Portal is required because widget cards use
// overflow-y-auto for internal scroll, which clips our 256px-tall tooltip.
import {
  Provider as TooltipProvider,
  Root as Tooltip,
  Trigger as TooltipTrigger,
  Portal as TooltipPortal,
  Content as TooltipContentPrimitive,
} from "@radix-ui/react-tooltip";
import { cn } from "@/lib/utils";
import { LabelImage } from "@/features/publicprofile/labels/LabelImage";
import type { LabelAward } from "@/features/publicprofile/labels/types";
import { formatCondition } from "@/features/publicprofile/labels/formatCondition";

export interface LabelChipProps {
  award: LabelAward;
  /** Visual density preset. `sm` is the hero tagline; `md` is the showcase widget. */
  size?: "sm" | "md";
  /** Optional cache-bust for the label image (typically an admin regen epoch). */
  bustHint?: string | number;
  /** Optional extra classes on the trigger chip. */
  className?: string;
  /** Streak count for this award (gaka-mwp-streaks). Values ≤ 1 render no
   *  badge. Values ≥ 2 render a small amber "Nx" pill in the top-right
   *  corner — "3x", "12x", etc. */
  streak?: number;
}

const CHIP_SIZE = {
  sm: {
    padding: "px-1.5 py-0.5",
    text: "text-[10px]",
    glyphPx: 14,
  },
  md: {
    padding: "px-2 py-1",
    text: "text-[10px]",
    glyphPx: 16,
  },
} as const;

export function LabelChip({
  award,
  size = "md",
  bustHint,
  className,
  streak,
}: LabelChipProps) {
  const sz = CHIP_SIZE[size];
  const fallback = award.glyph ? (
    <span aria-hidden className="opacity-80">
      {award.glyph}
    </span>
  ) : null;

  return (
    // Self-contained TooltipProvider: catalyst-ui bundles its own Radix so the
    // app's root TooltipProvider (from catalyst-ui) uses a DIFFERENT context
    // than these primitives (imported directly from @radix-ui/react-tooltip
    // for Portal access). Wrapping here scopes a compatible context for this
    // chip only; delayDuration=200 matches the app-wide feel.
    <TooltipProvider delayDuration={200}>
    <Tooltip>
      <TooltipTrigger asChild>
        {/*
          `asChild` hands the ref + accessibility props to our span so the
          tooltip anchors directly to the chip (no wrapper div). The chip
          text stays inline; hover/focus opens the popup.
        */}
        <span
          className={
            // Patches get a distinct visual: double-amber border (outline
            // shadow trick) + ★ prefix, so citations read differently
            // from the softer crimson chips used by tier/archetype/tribe/meme.
            // `relative` so the streak-count badge can absolute-position
            // to the top-right corner.
            "relative " +
            (award.kind === "patch"
              ? `inline-flex items-center gap-1 rounded-sm border border-amber-400/80 bg-amber-500/5 shadow-[0_0_0_1px_rgba(0,0,0,0.4),0_0_0_2px_rgba(245,166,35,0.35)] font-mono uppercase tracking-[0.14em] outline-none focus-visible:ring-1 focus-visible:ring-amber-400 ${sz.padding} ${sz.text}`
              : `inline-flex items-center gap-1 rounded-sm border border-[color:var(--primary)]/40 bg-[color:var(--primary)]/10 font-mono uppercase tracking-[0.12em] outline-none focus-visible:ring-1 focus-visible:ring-[color:var(--primary)] ${sz.padding} ${sz.text}`) +
            (className ? ` ${className}` : "")
          }
          data-testid="label-chip"
          data-label-id={award.id}
          data-label-kind={award.kind}
          data-streak={streak && streak > 1 ? streak : undefined}
          tabIndex={0}
        >
          {award.kind === "patch" && (
            <span aria-hidden className="text-amber-400">★</span>
          )}
          <LabelImage
            id={award.id}
            size={sz.glyphPx}
            bustHint={bustHint}
            className="opacity-90"
            fallback={fallback}
          />
          <span>{award.label}</span>
          {streak && streak > 1 ? (
            <span
              className="absolute -right-1 -top-1 rounded-sm border border-amber-400 bg-black px-1 py-[1px] text-[9px] font-bold leading-none text-amber-400 shadow-[0_0_4px_rgba(245,166,35,0.6)]"
              aria-label={`streak: ${streak} periods`}
              data-testid="label-streak-badge"
            >
              {streak}x
            </span>
          ) : null}
        </span>
      </TooltipTrigger>
      <TooltipPortal>
      <TooltipContentPrimitive
        side="top"
        align="center"
        sideOffset={8}
        // Replicates the styling catalyst-ui's TooltipContent adds on top of
        // Radix (animation, rounded corners, popover bg) since we're bypassing
        // that wrapper — see the import comment above for why.
        // z-50 keeps the portaled content above sheets / dialogs.
        className={cn(
          "z-50 max-w-[288px] p-0 rounded-md border border-[color:var(--primary)]/50 bg-[color:var(--card)] text-[color:var(--foreground)] shadow-lg",
          "animate-in fade-in-0 zoom-in-95",
          "data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95",
          "data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2",
        )}
        data-testid="label-chip-tooltip"
      >
        {/*
          Vertical stack: big square image on top (256px — actually reads the
          illustration instead of "is that a smudge?"), text block below. Prior
          layout had a 72px thumbnail beside the copy — user feedback: the
          images may as well not be there at that size.
        */}
        <div className="flex flex-col">
          <LabelImage
            id={award.id}
            size={256}
            bustHint={bustHint}
            className="h-64 w-full rounded-t-sm border-b border-[color:var(--primary)]/30 object-cover"
            fallback={
              <div
                aria-hidden
                className="flex h-64 w-full items-center justify-center rounded-t-sm border-b border-[color:var(--primary)]/30 bg-[color:var(--muted)] text-6xl"
              >
                {award.glyph ?? "·"}
              </div>
            }
          />
          <div className="flex flex-col gap-1.5 p-3">
            <div className="flex items-baseline justify-between gap-2">
              <div className="font-mono text-[12px] font-semibold uppercase tracking-[0.14em] text-[color:var(--primary)] leading-tight">
                {award.label}
              </div>
              {streak && streak > 1 ? (
                <div className="rounded-sm border border-amber-400 px-1.5 py-[1px] font-mono text-[10px] font-bold text-amber-400">
                  {streak}× streak
                </div>
              ) : null}
            </div>
            {award.description && (
              <div className="text-[11px] leading-snug text-[color:var(--foreground)]">
                {award.description}
              </div>
            )}
            {award.condition && (
              <div className="mt-1 border-t border-[color:var(--border)] pt-1.5">
                <div className="mb-0.5 font-mono text-[9px] uppercase tracking-[0.14em] text-[color:var(--muted-foreground)]">
                  Fires when
                </div>
                <div className="font-mono text-[10px] leading-snug text-amber-400/90">
                  {formatCondition(award.condition)}
                </div>
              </div>
            )}
            {award.tier && (
              <div className="mt-1 border-t border-[color:var(--border)] pt-1.5 text-[10px] leading-snug text-[color:var(--muted-foreground)]">
                <span className="uppercase tracking-[0.1em]">Tier: </span>
                <span className="font-mono text-[color:var(--foreground)] uppercase">
                  {award.tier}
                </span>
              </div>
            )}
          </div>
        </div>
      </TooltipContentPrimitive>
      </TooltipPortal>
    </Tooltip>
    </TooltipProvider>
  );
}
