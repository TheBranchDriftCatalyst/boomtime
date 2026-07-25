// GoalRing — Apple-Watch-style concentric rings for up to 3 goals
// (gaka-wpb). Reuses catalyst-ui's CircularGauge — one gauge per
// goal at a different radius to stack visually.
//
// Fewer than 3 goals renders the ones we have (a single goal
// becomes one big ring). More than 3 truncates — the tile is
// intentionally small and 4+ concentric rings become illegible.
import { CircularGauge } from "@thebranchdriftcatalyst/catalyst-ui/ui/circular-gauge";
import { useAllGoalProgress, useGoalsQuery } from "@/features/goals/useGoals";
import type { Goal, GoalProgress } from "@/types/api";

// Ring sizes: outer, middle, inner. Chosen so each ring's stroke
// leaves clear separation for the next. All at strokeWidth=8 for
// visual consistency.
const RING_SIZES = [128, 96, 64] as const;
const RING_VARIANTS = ["default", "success", "info"] as const;

export function GoalRing() {
  const { data: goals } = useGoalsQuery();
  const { data: batch } = useAllGoalProgress();

  const enabled = (goals ?? []).filter((g) => g.enabled).slice(0, 3);
  if (enabled.length === 0) {
    return (
      <div className="flex h-full w-full items-center justify-center px-3 text-center font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
        No goals yet
      </div>
    );
  }

  return (
    <div className="flex h-full w-full flex-col items-center justify-center gap-2">
      <div className="relative" style={{ width: 128, height: 128 }}>
        {enabled.map((g, i) => {
          const size = RING_SIZES[i];
          const pctVal = pctFor(g, batch?.progress?.[g.id]);
          return (
            <div
              key={g.id}
              className="absolute"
              style={{
                left: (128 - size) / 2,
                top: (128 - size) / 2,
              }}
            >
              <CircularGauge
                value={pctVal}
                size={size}
                strokeWidth={8}
                variant={RING_VARIANTS[i]}
                showPercent={false}
              />
            </div>
          );
        })}
      </div>
      {/* Legend so the reader knows which ring is which */}
      <ol className="w-full max-w-xs space-y-0.5 px-2">
        {enabled.map((g, i) => (
          <li
            key={g.id}
            className="flex items-center justify-between font-mono text-[10px] uppercase tracking-wider"
          >
            <span className="flex items-center gap-1.5 truncate">
              <span
                className="inline-block h-2 w-2 rounded-full"
                style={{ background: ringLegendColor(i) }}
                aria-hidden
              />
              <span className="truncate">{g.name}</span>
            </span>
            <span className="tabular-nums text-muted-foreground">
              {Math.round(pctFor(g, batch?.progress?.[g.id]))}%
            </span>
          </li>
        ))}
      </ol>
    </div>
  );
}

function pctFor(goal: Goal, progress: GoalProgress | undefined): number {
  const p = progress?.progress ?? goal.lastProgress?.progress ?? 0;
  return Math.round(p * 100);
}

// Cheap solid-color swatches matching the ring stroke colors above.
// Kept literal rather than pulling Tailwind classes because the
// legend is small enough that a manual palette keeps the visual
// association obvious.
function ringLegendColor(i: number): string {
  return ["hsl(var(--primary))", "rgb(34 197 94)", "rgb(59 130 246)"][i] ?? "gray";
}
