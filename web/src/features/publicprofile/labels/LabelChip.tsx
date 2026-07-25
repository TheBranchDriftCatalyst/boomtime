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
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/tooltip";
import { LabelImage } from "@/features/publicprofile/labels/LabelImage";
import type { LabelAward } from "@/features/publicprofile/labels/types";

export interface LabelChipProps {
  award: LabelAward;
  /** Visual density preset. `sm` is the hero tagline; `md` is the showcase widget. */
  size?: "sm" | "md";
  /** Optional cache-bust for the label image (typically an admin regen epoch). */
  bustHint?: string | number;
  /** Optional extra classes on the trigger chip. */
  className?: string;
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
}: LabelChipProps) {
  const sz = CHIP_SIZE[size];
  const fallback = award.glyph ? (
    <span aria-hidden className="opacity-80">
      {award.glyph}
    </span>
  ) : null;

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        {/*
          `asChild` hands the ref + accessibility props to our span so the
          tooltip anchors directly to the chip (no wrapper div). The chip
          text stays inline; hover/focus opens the popup.
        */}
        <span
          className={
            `inline-flex items-center gap-1 rounded-sm border border-[color:var(--primary)]/40 bg-[color:var(--primary)]/10 font-mono uppercase tracking-[0.12em] outline-none focus-visible:ring-1 focus-visible:ring-[color:var(--primary)] ${sz.padding} ${sz.text}` +
            (className ? ` ${className}` : "")
          }
          data-testid="label-chip"
          data-label-id={award.id}
          tabIndex={0}
        >
          <LabelImage
            id={award.id}
            size={sz.glyphPx}
            bustHint={bustHint}
            className="opacity-90"
            fallback={fallback}
          />
          <span>{award.label}</span>
        </span>
      </TooltipTrigger>
      <TooltipContent
        side="top"
        align="center"
        sideOffset={8}
        className="max-w-[280px] p-0 border border-[color:var(--primary)]/50 bg-[color:var(--card)] shadow-lg"
        data-testid="label-chip-tooltip"
      >
        <div className="flex gap-3 p-3">
          {/*
            The tooltip image is deliberately bigger (72px) than the chip
            glyph so the illustration actually reads. Same fallback rule —
            glyph if no generated image.
          */}
          <div className="shrink-0">
            <LabelImage
              id={award.id}
              size={72}
              bustHint={bustHint}
              className="rounded-sm border border-[color:var(--primary)]/30"
              fallback={
                <div
                  aria-hidden
                  className="flex h-[72px] w-[72px] items-center justify-center rounded-sm border border-[color:var(--primary)]/30 bg-[color:var(--muted)] text-2xl"
                >
                  {award.glyph ?? "·"}
                </div>
              }
            />
          </div>
          <div className="flex flex-col gap-1.5 min-w-0">
            <div className="font-mono text-[11px] font-semibold uppercase tracking-[0.14em] text-[color:var(--primary)] leading-tight">
              {award.label}
            </div>
            {award.description && (
              <div className="text-[11px] leading-snug text-[color:var(--foreground)]">
                {award.description}
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
      </TooltipContent>
    </Tooltip>
  );
}
