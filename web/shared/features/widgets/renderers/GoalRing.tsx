// GoalRing — Apple-Watch-style concentric rings for up to 3 goals (gaka-wpb).
//
// Drawn as ONE SVG of concentric progress arcs (not stacked CircularGauges —
// those each render a centered value, which overlapped into garbage at the
// shared center). No center text; the legend below carries the numbers.
//
// Fewer than 3 goals renders the ones we have; more than 3 truncates — the
// tile is intentionally small and 4+ rings become illegible.
import { useAllGoalProgress, useGoalsQuery } from "@boomtime/features/goals/useGoals";
import type { Goal, GoalProgress } from "@shared/types/api";

const BOX = 132;
const SW = 10;
// Outer → inner. Radii leave a clear gap (SW + ~4px) between rings. Colors are
// shared verbatim with the legend dots so ring ↔ label association is obvious.
const RINGS = [
  { r: 56, color: "var(--primary)" },
  { r: 42, color: "#22c55e" },
  { r: 28, color: "#3b82f6" },
] as const;

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
    <div className="flex h-full w-full flex-col items-center justify-center gap-2 overflow-hidden">
      <svg
        viewBox={`0 0 ${BOX} ${BOX}`}
        className="min-h-0 w-auto shrink"
        style={{ height: "62%", maxWidth: "100%", aspectRatio: "1" }}
        role="img"
        aria-label="Goal progress rings"
      >
        {/* -90° so each arc starts at 12 o'clock and fills clockwise. */}
        <g transform={`rotate(-90 ${BOX / 2} ${BOX / 2})`}>
          {enabled.map((g, i) => {
            const { r, color } = RINGS[i];
            const pct = Math.min(100, pctFor(g, batch?.progress?.[g.id]));
            const circ = 2 * Math.PI * r;
            const offset = circ * (1 - pct / 100);
            return (
              <g key={g.id}>
                {/* track */}
                <circle
                  cx={BOX / 2}
                  cy={BOX / 2}
                  r={r}
                  fill="none"
                  stroke="var(--muted-foreground)"
                  strokeWidth={SW}
                  opacity={0.14}
                />
                {/* progress */}
                <circle
                  cx={BOX / 2}
                  cy={BOX / 2}
                  r={r}
                  fill="none"
                  stroke={color}
                  strokeWidth={SW}
                  strokeLinecap="round"
                  strokeDasharray={circ}
                  strokeDashoffset={offset}
                  style={{ transition: "stroke-dashoffset 0.6s ease" }}
                />
              </g>
            );
          })}
        </g>
      </svg>

      {/* Legend so the reader knows which ring is which. */}
      <ol className="w-full max-w-xs shrink-0 space-y-0.5 px-2">
        {enabled.map((g, i) => (
          <li
            key={g.id}
            className="flex items-center justify-between gap-2 font-mono text-[10px] uppercase tracking-wider"
          >
            <span className="flex min-w-0 items-center gap-1.5">
              <span
                className="inline-block h-2 w-2 shrink-0 rounded-full"
                style={{ background: RINGS[i].color }}
                aria-hidden
              />
              <span className="truncate">{g.name}</span>
            </span>
            <span className="shrink-0 tabular-nums text-muted-foreground">
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
