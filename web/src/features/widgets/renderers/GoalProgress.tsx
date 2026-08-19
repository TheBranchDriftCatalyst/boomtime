// GoalProgress — one goal, horizontal bar with % + name (gaka-wpb).
//
// Fetches from the batched /goals/progress endpoint (single HTTP
// round trip serves every goal tile on the dashboard). Picks the
// first goal in the caller's list by default; a future v2 could
// let the user pin a specific goal id via the layout entry's
// existing `view` slot.
//
// If the API returns nothing (goal not present, unauth 401 on
// public view, network error), renders a neutral placeholder — a
// missing goal shouldn't crash the whole dashboard.
import { Progress } from "@thebranchdriftcatalyst/catalyst-ui/ui/progress";
import { useAllGoalProgress, useGoalsQuery } from "@boomtime/features/goals/useGoals";
import type { Goal, GoalProgress as GoalProgressT } from "@/types/api";

export function GoalProgress() {
  const { data: goals } = useGoalsQuery();
  const { data: batch } = useAllGoalProgress();

  const enabled = (goals ?? []).filter((g) => g.enabled);
  const goal = enabled[0];

  if (!goal) {
    return <Placeholder note="No goals yet — create one in Settings > Goals" />;
  }
  const prog = batch?.progress?.[goal.id];
  if (!prog) {
    // Cache hydrating — show the persisted last_progress if we have it.
    return <RenderOne goal={goal} progress={goal.lastProgress ?? undefined} />;
  }
  return <RenderOne goal={goal} progress={prog} />;
}

function RenderOne({
  goal,
  progress,
}: {
  goal: Goal;
  progress: GoalProgressT | undefined;
}) {
  const pct = Math.round((progress?.progress ?? 0) * 100);
  const hit = progress?.hit ?? false;
  return (
    <div className="flex h-full flex-col justify-center gap-2 px-3">
      <div className="flex items-baseline justify-between">
        <span className="truncate font-mono text-sm font-medium">{goal.name}</span>
        <span
          className={
            "font-mono text-xs tabular-nums " +
            (hit ? "text-emerald-400" : "text-muted-foreground")
          }
        >
          {pct}%
        </span>
      </div>
      {goal.description && (
        <p className="truncate text-xs text-muted-foreground">
          {goal.description}
        </p>
      )}
      <Progress value={pct} />
    </div>
  );
}

function Placeholder({ note }: { note: string }) {
  return (
    <div className="flex h-full w-full items-center justify-center px-3 text-center font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
      {note}
    </div>
  );
}
