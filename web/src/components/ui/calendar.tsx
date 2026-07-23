import { ChevronLeft, ChevronRight } from "lucide-react";
import { DayPicker } from "react-day-picker";
import "react-day-picker/style.css";
import { cn } from "@/lib/utils";

export type CalendarProps = React.ComponentProps<typeof DayPicker>;

/*
 * react-day-picker v9 exposes theming through CSS custom properties (--rdp-*).
 * Its default stylesheet ships with a bright pale-blue accent that renders as
 * near-white against boomtime's dark background, making the range-middle day
 * numbers unreadable. We rewire the tokens onto the theme's own vars so the
 * calendar picks up light/dark automatically:
 *   --rdp-accent-color              → primary (magenta, for start/end circles)
 *   --rdp-accent-background-color   → primary @ ~18% alpha (range middle bg)
 *   --rdp-range_middle-color        → foreground (legible day numbers)
 *
 * `color-mix` lets us dim primary just enough to feel "selected but muted"
 * against the popover surface in both dark and light variants.
 */
const rdpVars = {
  "--rdp-accent-color": "var(--primary)",
  "--rdp-accent-background-color":
    "color-mix(in oklch, var(--primary) 18%, transparent)",
  "--rdp-range_middle-background-color":
    "color-mix(in oklch, var(--primary) 18%, transparent)",
  "--rdp-range_middle-color": "var(--foreground)",
  "--rdp-range_start-color": "var(--primary-foreground)",
  "--rdp-range_end-color": "var(--primary-foreground)",
  "--rdp-range_start-date-background-color": "var(--primary)",
  "--rdp-range_end-date-background-color": "var(--primary)",
  "--rdp-selected-border": "2px solid var(--primary)",
  "--rdp-today-color": "var(--primary)",
} as React.CSSProperties;

function Calendar({ className, classNames, style, ...props }: CalendarProps) {
  return (
    <DayPicker
      className={cn("p-1", className)}
      classNames={classNames}
      style={{ ...rdpVars, ...style }}
      components={{
        Chevron: ({ orientation }) =>
          orientation === "left" ? (
            <ChevronLeft className="h-4 w-4" />
          ) : (
            <ChevronRight className="h-4 w-4" />
          ),
      }}
      {...props}
    />
  );
}

export { Calendar };
