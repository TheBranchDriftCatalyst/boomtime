// GoalNearnessStrip — an at-a-glance "punchcard" of every goal's nearness to
// completion (gaka-cl9). One cell per goal, filled by the 0..1 progress scalar
// (the evaluator's min-over-AND / max-over-OR rollup); goals that are HIT are
// accented emerald. PAUSED (disabled) goals are shown dimmed — the batch
// progress endpoint skips them, so they render "—" unless a lastProgress
// snapshot exists (gaka-* fix: previously the strip filtered to enabled goals,
// so a user whose only goal was paused saw NO card at all). Slots at the top of
// the Goals page so you can see which goals are hot (near done) vs cold in one
// dense row, before scanning the list.
//
// Data: reuses the BATCHED /goals/progress call via useAllGoalProgress — the
// same react-query key GoalsList uses, so this adds no extra fetch. The fill is
// a `bg-primary` layer at a dynamic opacity (theme-safe: it resolves the
// primary color regardless of its HSL/oklch format, and the opacity lives on a
// separate layer so the % label stays fully legible).
import { Radar } from "lucide-react";
import { useAllGoalProgress } from "@boomtime/features/goals/useGoals";
import { cn } from "@shared/lib/utils";
import type { Goal } from "@shared/types/api";

export function GoalNearnessStrip({ goals }: { goals: Goal[] }) {
  const { data: batch } = useAllGoalProgress();
  if (goals.length === 0) return null;
  const pausedCount = goals.filter((g) => !g.enabled).length;

  return (
    <div className="rounded-xl border bg-card/50 p-4 shadow-sm">
      <div className="mb-3.5 flex items-baseline justify-between gap-2">
        <h3 className="flex items-center gap-1.5 text-sm font-semibold">
          <Radar className="h-4 w-4 text-primary" />
          Nearness
        </h3>
        <span className="text-xs text-muted-foreground">
          {goals.length} goal{goals.length === 1 ? "" : "s"}
          {pausedCount > 0 ? ` · ${pausedCount} paused` : ""} · hover for detail
        </span>
      </div>
      <div className="flex flex-wrap gap-x-4 gap-y-3">
        {goals.map((g) => {
          const prog = batch?.progress?.[g.id];
          const raw = prog?.progress ?? g.lastProgress?.progress;
          const hasData = raw != null;
          const frac = Math.min(1, Math.max(0, raw ?? 0));
          const pct = Math.round(frac * 100);
          const hit = prog?.hit ?? g.lastProgress?.hit ?? false;
          const paused = !g.enabled;
          return (
            <div
              key={g.id}
              className={cn("flex w-16 flex-col items-center gap-1.5", paused && "opacity-50")}
            >
              <div
                title={`${g.name} — ${hasData ? `${pct}%` : "no progress yet"}${
                  hit ? " · hit!" : ""
                }${paused ? " · paused" : ""}`}
                className={cn(
                  "relative flex h-16 w-16 items-center justify-center overflow-hidden rounded-xl border transition-colors",
                  hit && !paused
                    ? "border-emerald-500/50 ring-1 ring-emerald-500/40"
                    : "border-border/60 hover:border-primary/40",
                )}
              >
                {/* nearness fill — separate layer so the % label stays legible */}
                <span
                  aria-hidden
                  className="absolute inset-0 bg-primary"
                  style={{ opacity: hasData ? 0.1 + 0.85 * frac : 0 }}
                />
                <span
                  className={cn(
                    "relative z-10 text-xs font-semibold tabular-nums",
                    hit && !paused ? "text-emerald-100" : "text-foreground/90",
                  )}
                >
                  {hasData ? `${pct}%` : "—"}
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
