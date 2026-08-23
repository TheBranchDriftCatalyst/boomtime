// ReadingRangeControl — the segmented time-window switcher in the Reading
// dashboard header (boom-h2pg). Drives the windowed tiles (listening in range,
// trend, finished-per-bucket) through the shared `readingRange` store. Styled
// as a neon pill group to match the house synthwave chrome and the Books-page
// Table/Explore toggle; theme-aware (uses the primary token, so it re-tints per
// theme).
import { useReadingRange, READING_RANGE_PRESETS } from "./readingRange";

export function ReadingRangeControl() {
  const [preset, setPreset] = useReadingRange();

  return (
    <div
      role="radiogroup"
      aria-label="Stats window"
      className="inline-flex items-center gap-0.5 rounded-md border border-primary/20 bg-primary/5 p-0.5"
      data-testid="reading-range-control"
    >
      {READING_RANGE_PRESETS.map((p) => {
        const active = p.key === preset.key;
        return (
          <button
            key={p.key}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => setPreset(p.key)}
            className={
              "rounded px-2.5 py-1 text-xs font-medium tabular-nums transition-colors " +
              (active
                ? "bg-primary/15 text-primary shadow-[0_0_8px_hsl(var(--primary)/0.35)]"
                : "text-muted-foreground hover:text-foreground hover:bg-primary/10")
            }
          >
            {p.label}
          </button>
        );
      })}
    </div>
  );
}
