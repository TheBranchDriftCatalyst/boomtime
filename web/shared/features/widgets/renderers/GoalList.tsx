// GoalList — compact list of all enabled goals with tiny progress
// bars per row (boom-wpb). Same batched query as the other goal
// renderers so a dashboard rendering all three shares one round
// trip.
import { Progress } from "@thebranchdriftcatalyst/catalyst-ui/ui/progress";
import { useAllGoalProgress, useGoalsQuery } from "@boomtime/features/goals/useGoals";

export function GoalList() {
  const { data: goals } = useGoalsQuery();
  const { data: batch } = useAllGoalProgress();

  const enabled = (goals ?? []).filter((g) => g.enabled);
  if (enabled.length === 0) {
    return (
      <div className="flex h-full w-full items-center justify-center px-3 text-center font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
        No goals yet
      </div>
    );
  }

  return (
    <ol className="flex h-full w-full flex-col gap-1 overflow-y-auto p-2">
      {enabled.map((g) => {
        const prog = batch?.progress?.[g.id] ?? g.lastProgress ?? undefined;
        const pct = Math.round((prog?.progress ?? 0) * 100);
        const hit = prog?.hit ?? false;
        return (
          <li key={g.id} className="flex flex-col gap-0.5">
            <div className="flex items-baseline justify-between font-mono text-[10px] uppercase tracking-[0.1em]">
              <span className="truncate text-muted-foreground">{g.name}</span>
              <span
                className={
                  "tabular-nums " +
                  (hit ? "text-emerald-400" : "text-muted-foreground")
                }
              >
                {pct}%
              </span>
            </div>
            <Progress value={pct} className="h-1.5" />
          </li>
        );
      })}
    </ol>
  );
}
