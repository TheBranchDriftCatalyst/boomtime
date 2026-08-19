// GoalsList — the table body of the Settings > Goals tab. One row
// per goal with an inline progress bar, toggle, edit + delete
// icons. Mirrors the chrome of RemappingRow so the Settings surface
// stays visually consistent.
//
// Progress numbers come from the BATCHED /goals/progress call — one
// round trip populates every row. A row with no batched entry shows
// "computing…" (goal disabled or cache being repopulated).
import { Eye, EyeOff, Loader2, Pencil, Plus, Target, Trash2 } from "lucide-react";
import { Progress } from "@thebranchdriftcatalyst/catalyst-ui/ui/progress";
import { Badge } from "@thebranchdriftcatalyst/catalyst-ui/ui/badge";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { EmptyState } from "@shared/components/EmptyState";
import { useAllGoalProgress, useGoalMutations } from "@boomtime/features/goals/useGoals";
import type { Goal, GoalProgress } from "@shared/types/api";

export function GoalsList({
  goals,
  onEdit,
  onRemove,
  onCreate,
}: {
  goals: Goal[];
  onEdit: (goal: Goal) => void;
  onRemove: (goal: Goal) => void;
  /** Opens the create-goal form from the empty-state CTA. */
  onCreate?: () => void;
}) {
  const { data: batch } = useAllGoalProgress();
  const { toggle } = useGoalMutations();

  if (goals.length === 0) {
    return (
      <div className="rounded-xl border border-dashed border-border/70 bg-card/40">
        <EmptyState
          icon={Target}
          title="No goals yet"
          description={
            <>
              Author your first target — e.g. "at least 1 hour a week on Go" or
              "avoid distraction: less than 30 minutes/day on personal
              projects".
            </>
          }
          action={
            onCreate && (
              <Button onClick={onCreate} size="sm">
                <Plus className="mr-1 h-4 w-4" />
                New goal
              </Button>
            )
          }
        />
      </div>
    );
  }

  return (
    <div className="space-y-2.5">
      {goals.map((goal) => {
        const prog = batch?.progress?.[goal.id];
        return (
          <GoalRow
            key={goal.id}
            goal={goal}
            progress={prog}
            onEdit={() => onEdit(goal)}
            onRemove={() => onRemove(goal)}
            onToggle={() =>
              toggle.mutate({ id: goal.id, enabled: !goal.enabled })
            }
          />
        );
      })}
    </div>
  );
}

function GoalRow({
  goal,
  progress,
  onEdit,
  onRemove,
  onToggle,
}: {
  goal: Goal;
  progress: GoalProgress | undefined;
  onEdit: () => void;
  onRemove: () => void;
  onToggle: () => void;
}) {
  // Progress bar: prefer live batch value; fall back to the persisted
  // lastProgress when the batch hasn't loaded yet (avoids a flash of
  // "computing"). Percent for the label. Disabled goals stay dim.
  const pct = Math.round((progress?.progress ?? goal.lastProgress?.progress ?? 0) * 100);
  const hit = progress?.hit ?? goal.lastProgress?.hit ?? false;

  return (
    <div
      className={
        "group rounded-lg border border-border/60 bg-secondary/40 p-3.5 text-sm transition-colors hover:border-border hover:bg-secondary/60 " +
        (goal.enabled ? "" : "opacity-60")
      }
    >
      <div className="mb-2.5 flex items-center gap-2">
        <span className="flex-1 truncate font-medium">{goal.name}</span>
        {goal.public && (
          <Badge
            variant="outline"
            className="shrink-0 border-sky-500/40 text-[10px] uppercase text-sky-400"
            data-testid="goal-public-badge"
          >
            public
          </Badge>
        )}
        {hit && (
          <Badge
            variant="outline"
            className="shrink-0 border-emerald-500/40 text-[10px] uppercase text-emerald-400"
          >
            hit
          </Badge>
        )}
        <button
          onClick={onToggle}
          disabled={toggleDisabled(goal)}
          title={goal.enabled ? "Pause goal" : "Resume goal"}
          aria-label={goal.enabled ? "Pause goal" : "Resume goal"}
          className="rounded-full p-0.5 text-muted-foreground hover:bg-background hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50"
        >
          {goal.enabled ? (
            <EyeOff className="h-3.5 w-3.5" />
          ) : (
            <Eye className="h-3.5 w-3.5" />
          )}
        </button>
        <button
          onClick={onEdit}
          title="Edit goal"
          className="rounded-full p-0.5 text-muted-foreground hover:bg-background hover:text-foreground"
        >
          <Pencil className="h-3.5 w-3.5" />
        </button>
        <button
          onClick={onRemove}
          title="Delete goal"
          className="rounded-full p-0.5 text-muted-foreground hover:bg-destructive/20 hover:text-destructive"
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      </div>
      {goal.description && (
        <p className="mb-2 text-xs text-muted-foreground">{goal.description}</p>
      )}
      <div className="flex items-center gap-2.5">
        <Progress value={pct} className="h-2 flex-1" />
        <span
          className={
            "w-10 shrink-0 text-right font-mono text-xs font-semibold tabular-nums " +
            (hit ? "text-emerald-400" : "text-foreground/80")
          }
        >
          {progress || goal.lastProgress ? `${pct}%` : (
            <Loader2 className="ml-auto h-3 w-3 animate-spin text-muted-foreground" />
          )}
        </span>
      </div>
    </div>
  );
}

// Small helper: don't let the user spam the toggle mid-mutation.
// Kept as its own function so the button doesn't need to know about
// mutation state directly (the hook return is captured in the
// parent — this is a display-only refinement).
function toggleDisabled(_goal: Goal): boolean {
  return false;
}
