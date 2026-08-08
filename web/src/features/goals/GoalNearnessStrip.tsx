// GoalNearnessStrip — an at-a-glance "punchcard" of every goal's nearness to
// completion (gaka-cl9). One cell per ACTIVE goal, filled by the 0..1 progress
// scalar (the evaluator's min-over-AND / max-over-OR rollup); goals that are HIT
// are accented emerald. Slots at the top of the Goals page so you can see which
// goals are hot (near done) vs cold in one dense row, before scanning the list.
//
// Data: reuses the BATCHED /goals/progress call via useAllGoalProgress — the
// same react-query key GoalsList uses, so this adds no extra fetch. The fill is
// a `bg-primary` layer at a dynamic opacity (theme-safe: it resolves the
// primary color regardless of its HSL/oklch format, and the opacity lives on a
// separate layer so the % label stays fully legible).
import { useAllGoalProgress } from "@/features/goals/useGoals";
import { cn } from "@/lib/utils";
import type { Goal } from "@/types/api";

export function GoalNearnessStrip({ goals }: { goals: Goal[] }) {
  const { data: batch } = useAllGoalProgress();
  const active = goals.filter((g) => g.enabled);
  if (active.length === 0) return null;

  return (
    <div className="rounded-xl border bg-card/50 p-4">
      <div className="mb-3 flex items-baseline justify-between gap-2">
        <h3 className="text-sm font-semibold">Nearness</h3>
        <span className="text-xs text-muted-foreground">
          {active.length} active goal{active.length === 1 ? "" : "s"} · hover for detail
        </span>
      </div>
      <div className="flex flex-wrap gap-3">
        {active.map((g) => {
          const prog = batch?.progress?.[g.id];
          const frac = Math.min(1, Math.max(0, prog?.progress ?? g.lastProgress?.progress ?? 0));
          const pct = Math.round(frac * 100);
          const hit = prog?.hit ?? g.lastProgress?.hit ?? false;
          return (
            <div key={g.id} className="flex w-16 flex-col items-center gap-1">
              <div
                title={`${g.name} — ${pct}%${hit ? " · hit!" : ""}`}
                className={cn(
                  "relative flex h-14 w-14 items-center justify-center overflow-hidden rounded-lg border transition-colors",
                  hit ? "border-emerald-500/50 ring-1 ring-emerald-500/40" : "border-border/60",
                )}
              >
                {/* nearness fill — separate layer so the % label stays legible */}
                <span
                  aria-hidden
                  className="absolute inset-0 bg-primary"
                  style={{ opacity: 0.1 + 0.85 * frac }}
                />
                <span
                  className={cn(
                    "relative z-10 text-xs font-semibold tabular-nums",
                    hit ? "text-emerald-100" : "text-foreground/90",
                  )}
                >
                  {pct}%
                </span>
              </div>
              <span
                className="w-full truncate text-center text-[10px] text-muted-foreground"
                title={g.name}
              >
                {g.name}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}
