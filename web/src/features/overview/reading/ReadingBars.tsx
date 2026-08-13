// ReadingBars — a compact horizontal bar list for categorical reading data
// ({label,value}[]): top-series-by-runtime, prolific-genres, finished-per-month.
//
// Deliberately CSS/flex (not SVG): the labels + values are real DOM text, so the
// Reading tiles' tests can assert the mapped data verbatim, and a long list
// scrolls INTERNALLY (max-height + overflow-y) instead of blowing out the tile.
// Bars fill relative to the max value; color defaults to the neon primary with a
// per-row override so the genre tiles can share the house `colorAt` palette.
import { EmptyChart } from "@/viz/d3/EmptyChart";

export interface ReadingBar {
  label: string;
  value: number;
  /** Optional per-bar fill (rgb from `colorAt`); defaults to the primary neon. */
  color?: string;
  /** Optional richer title on hover. */
  title?: string;
}

interface ReadingBarsProps {
  data: ReadingBar[];
  /** Format the trailing value readout (e.g. runtime → "12h 30m", count → "7"). */
  valueFmt: (v: number) => string;
  /** Panel height; the list scrolls internally past it. */
  height?: number;
  emptyHint?: string;
}

export function ReadingBars({
  data,
  valueFmt,
  height = 280,
  emptyHint,
}: ReadingBarsProps) {
  if (data.length === 0) {
    return <EmptyChart height={height} hint={emptyHint} />;
  }
  const max = Math.max(...data.map((d) => d.value), 1);

  return (
    <div
      className="flex flex-col gap-2.5 overflow-y-auto pr-1"
      style={{ maxHeight: height }}
      data-testid="reading-bars"
    >
      {data.map((d, i) => {
        const pct = Math.max(2, (d.value / max) * 100);
        return (
          <div key={`${d.label}-${i}`} className="flex flex-col gap-1" data-testid="reading-bar">
            <div className="flex items-baseline justify-between gap-3 text-sm">
              <span className="truncate text-foreground/90" title={d.title ?? d.label}>
                {d.label}
              </span>
              <span className="shrink-0 tabular-nums font-medium text-muted-foreground">
                {valueFmt(d.value)}
              </span>
            </div>
            <div className="h-2 w-full overflow-hidden rounded-full bg-muted/40">
              <div
                data-testid="reading-bar-fill"
                className="h-full rounded-full transition-[width] duration-500"
                style={{
                  width: `${pct}%`,
                  background: d.color
                    ? d.color
                    : "linear-gradient(90deg, hsl(var(--primary)/0.55), hsl(var(--primary)))",
                  boxShadow: "0 0 8px hsl(var(--primary)/0.35)",
                }}
              />
            </div>
          </div>
        );
      })}
    </div>
  );
}
