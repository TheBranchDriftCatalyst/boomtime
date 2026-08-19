// WeeklyListeningGoal — a progress ring for the user's weekly listening goal,
// or a "set a listening goal" CTA when none exists. Read-only against the goals
// feature: it reuses the existing useGoalsQuery + useGoalProgress hooks (no new
// endpoints, no writes). A "listening goal" is any enabled goal whose predicate
// tree contains a `time` leaf with source "reading" over a "week" window.
import { useMemo } from "react";
import { Link } from "react-router";
import { Headphones, Target } from "lucide-react";
import { ChartCard } from "@/components/ChartCard";
import { Spinner } from "@thebranchdriftcatalyst/catalyst-ui/ui/spinner";
import { useGoalsQuery, useGoalProgress } from "@boomtime/features/goals/useGoals";
import type { Goal, Predicate } from "@/types/api";

const HEIGHT = 260;

/** Does this predicate tree contain a reading-domain, weekly `time` leaf? */
function hasWeeklyListeningLeaf(p: Predicate): boolean {
  switch (p.kind) {
    case "time":
      return p.source === "reading" && p.window === "week";
    case "streak":
      return hasWeeklyListeningLeaf(p.condition);
    case "all":
    case "any":
      return p.of.some(hasWeeklyListeningLeaf);
    case "not":
      return hasWeeklyListeningLeaf(p.of[0]);
    default:
      return false;
  }
}

function ProgressRing({ frac, hit }: { frac: number; hit: boolean }) {
  const size = 140;
  const stroke = 12;
  const r = (size - stroke) / 2;
  const c = 2 * Math.PI * r;
  const clamped = Math.min(1, Math.max(0, frac));
  const pct = Math.round(clamped * 100);
  const accent = hit ? "rgb(16 185 129)" : "hsl(var(--primary))";
  return (
    <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} role="img" aria-label={`Weekly listening goal ${pct}%`}>
      <circle
        cx={size / 2}
        cy={size / 2}
        r={r}
        fill="none"
        stroke="hsl(var(--muted))"
        strokeWidth={stroke}
        opacity={0.4}
      />
      <circle
        cx={size / 2}
        cy={size / 2}
        r={r}
        fill="none"
        stroke={accent}
        strokeWidth={stroke}
        strokeLinecap="round"
        strokeDasharray={c}
        strokeDashoffset={c * (1 - clamped)}
        transform={`rotate(-90 ${size / 2} ${size / 2})`}
        style={{ filter: `drop-shadow(0 0 6px ${accent})`, transition: "stroke-dashoffset .6s ease" }}
      />
      <text
        x="50%"
        y="50%"
        dominantBaseline="central"
        textAnchor="middle"
        className="fill-foreground"
        style={{ fontSize: 26, fontWeight: 700 }}
      >
        {pct}%
      </text>
    </svg>
  );
}

function GoalRing({ goal }: { goal: Goal }) {
  const prog = useGoalProgress(goal.id);
  const frac = prog.data?.progress ?? goal.lastProgress?.progress ?? 0;
  const hit = prog.data?.hit ?? goal.lastProgress?.hit ?? false;
  return (
    <div
      className="flex flex-col items-center justify-center gap-3 text-center"
      style={{ minHeight: HEIGHT }}
      data-testid="listening-goal-ring"
    >
      <ProgressRing frac={frac} hit={hit} />
      <div>
        <div className="flex items-center justify-center gap-1.5 text-sm font-medium">
          <Headphones className="h-4 w-4 text-primary" />
          {goal.name}
        </div>
        <p className="mt-0.5 text-xs text-muted-foreground">
          {hit ? "Goal hit this week — nice." : "Weekly listening goal"}
        </p>
      </div>
    </div>
  );
}

function SetGoalCta() {
  return (
    <div
      className="flex flex-col items-center justify-center gap-3 px-4 text-center"
      style={{ minHeight: HEIGHT }}
      data-testid="listening-goal-cta"
    >
      <span className="flex h-14 w-14 items-center justify-center rounded-full bg-primary/10 ring-1 ring-primary/20">
        <Target className="h-6 w-6 text-primary" />
      </span>
      <p className="max-w-xs text-sm text-muted-foreground">
        Set a weekly listening goal to track your progress toward a habit.
      </p>
      <Link
        to="/app/goals"
        className="inline-flex items-center gap-1.5 rounded-full border border-primary/30 bg-primary/5 px-3 py-1.5 text-sm text-foreground/90 transition-colors hover:border-primary/50 hover:bg-primary/10 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/50"
      >
        <Target className="h-4 w-4 text-primary" />
        Set a listening goal
      </Link>
    </div>
  );
}

export function WeeklyListeningGoalTile() {
  const goalsQuery = useGoalsQuery();
  const goal = useMemo(
    () =>
      (goalsQuery.data ?? []).find(
        (g) => g.enabled && hasWeeklyListeningLeaf(g.spec),
      ),
    [goalsQuery.data],
  );

  return (
    <ChartCard title="Weekly listening goal">
      {goalsQuery.isLoading ? (
        <div className="flex items-center justify-center" style={{ height: HEIGHT }}>
          <Spinner />
        </div>
      ) : goal ? (
        <GoalRing goal={goal} />
      ) : (
        <SetGoalCta />
      )}
    </ChartCard>
  );
}
