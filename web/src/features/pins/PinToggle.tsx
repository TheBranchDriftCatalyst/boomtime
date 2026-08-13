// PinToggle — a small icon button that pins/unpins a single (axis, value) as a
// canonical entity (gaka-canon). A pin forces that value to always get its own
// slice/bar and never fall into the bucket "Other"; the backend query engine
// auto-applies pins, so on click we just create/remove the rule and let usePins
// invalidate the grouped-query caches (the chart refetches and the value
// visibly escapes Other).
//
// Tasteful + synthwave: unpinned reads as a muted ghost pin that lights up on
// hover; pinned reads as a filled neon-primary pin with a soft glow. a11y:
// a real <button> with aria-pressed + a descriptive title/aria-label.
import { Pin } from "lucide-react";
import { cn } from "@/lib/utils";
import { usePins } from "./usePins";

interface PinToggleProps {
  /** The grouping dimension this value belongs to (e.g. "genre", "author"). */
  axis: string;
  /** The dimension value to pin (e.g. "Fantasy", "Brandon Sanderson"). */
  value: string;
  className?: string;
}

export function PinToggle({ axis, value, className }: PinToggleProps) {
  const { pinFor, pin, unpin } = usePins();
  const rule = pinFor(axis, value);
  const pinned = Boolean(rule);
  const busy = pin.isPending || unpin.isPending;

  function toggle() {
    if (rule) {
      unpin.mutate(rule.id);
    } else {
      pin.mutate({ axis, value });
    }
  }

  return (
    <button
      type="button"
      data-testid="pin-toggle"
      aria-pressed={pinned}
      aria-label={pinned ? `Unpin ${value}` : `Pin ${value}`}
      title={
        pinned
          ? `Unpin "${value}" — let it fall into Other again`
          : `Pin "${value}" — always give it its own slice`
      }
      disabled={busy}
      onClick={toggle}
      className={cn(
        "inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-md transition-all",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/60",
        "disabled:cursor-not-allowed disabled:opacity-50",
        pinned
          ? "text-primary drop-shadow-[0_0_6px_hsl(var(--primary)/0.7)] hover:bg-primary/10"
          : "text-muted-foreground/50 hover:bg-muted/40 hover:text-foreground",
        className,
      )}
    >
      <Pin
        className={cn("h-3.5 w-3.5 transition-transform", pinned && "fill-current")}
        // A pinned pin sits upright; unpinned tips back to read as "click me".
        style={{ transform: pinned ? "rotate(0deg)" : "rotate(-30deg)" }}
        aria-hidden
      />
    </button>
  );
}

export default PinToggle;
