import { useMemo } from "react";
import { Activity, Flame, Footprints, HeartPulse, Moon } from "lucide-react";
import { ChartCard } from "@/components/ChartCard";
import { Card, CardContent } from "@/components/ui/card";
import type { HealthActivityPayload } from "@/types/api";

interface WellnessCardProps {
  data: HealthActivityPayload | undefined;
}

// Apple Watch / HealthKit overlay for the Overview page. Sibling to
// AIAssistanceCard — both are ambient overlays that render nothing when the
// range holds no data (payload.hasData=false).
//
// Design: five mini-stat tiles (Workouts today / Avg HR / Steps / Active kcal /
// Sleep) plus a workout-vs-idle-waking horizontal ratio bar. The bar splits
// the day's 16 waking hours between "moving" (workoutMinutes + a proxy from
// step-derived activity) and "at rest". Sleep is separately called out as a
// tile so it's not double-counted.
//
// Deep-dive charts (per-second HR series, HR zones, sleep stage breakdown,
// day-of-week overlays) live on the dedicated /wellness page — this card is
// scoped to the answer to "did the body move at all this week?".
export function WellnessCard({ data }: WellnessCardProps) {
  const summary = useMemo(() => {
    if (!data || !data.hasData) return null;
    // Prefer the "most recent day with any workout" for the ratio bar — showing
    // today when today has no data is less informative than showing the last
    // real workout.
    const latest =
      [...data.days].reverse().find((d) => d.workouts > 0) ??
      data.days[data.days.length - 1];
    const totals = data.totals;
    const moveMin = totals.workoutMinutes;
    // Cheap "waking minutes" proxy: 24h - avg sleep hours across the range,
    // clamped to a sane band so a range with no sleep samples doesn't imply
    // 24 waking hours × N days.
    const sleepHours = totals.sleepMinutes / 60;
    const daysCount = Math.max(data.days.length, 1);
    const avgWakingHoursPerDay = Math.min(
      Math.max(24 - sleepHours / daysCount, 12),
      20,
    );
    const wakingMin = avgWakingHoursPerDay * 60 * daysCount;
    const movePct = wakingMin > 0 ? Math.min(100, (moveMin / wakingMin) * 100) : 0;
    return {
      latest,
      totals,
      movePct,
      restPct: 100 - movePct,
    };
  }, [data]);

  if (!data || !data.hasData || !summary) return null;

  const { latest, totals, movePct, restPct } = summary;

  return (
    <ChartCard
      title="Wellness"
      action={
        <span className="text-xs text-muted-foreground">
          <HeartPulse className="mr-1 inline h-3 w-3" />
          Apple Watch
        </span>
      }
    >
      <div className="space-y-4">
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-5">
          <MiniStat
            label="Workouts"
            value={fmt(totals.workouts)}
            icon={Activity}
            hint={
              latest.workouts > 0
                ? `${fmt(latest.workouts)} on ${formatShortDay(latest.day)}`
                : "no workouts logged"
            }
          />
          <MiniStat
            label="Avg HR"
            value={totals.avgHR > 0 ? `${Math.round(totals.avgHR)} bpm` : "—"}
            icon={HeartPulse}
            hint={
              totals.restingHR > 0
                ? `resting ~${Math.round(totals.restingHR)}`
                : undefined
            }
          />
          <MiniStat
            label="Steps"
            value={fmt(totals.steps)}
            icon={Footprints}
            hint={`~${fmt(Math.round(totals.steps / Math.max(data.days.length, 1)))} / day`}
          />
          <MiniStat
            label="Active kcal"
            value={fmt(Math.round(totals.activeKcal))}
            icon={Flame}
            hint={`${fmt(Math.round(totals.workoutMinutes))} min moved`}
          />
          <MiniStat
            label="Sleep"
            value={
              totals.sleepMinutes > 0
                ? `${(totals.sleepMinutes / 60 / Math.max(data.days.length, 1)).toFixed(1)} h`
                : "—"
            }
            icon={Moon}
            hint={
              totals.hrvMs > 0
                ? `HRV ~${Math.round(totals.hrvMs)} ms`
                : "avg per night"
            }
          />
        </div>

        <div className="space-y-1.5">
          <div className="flex justify-between text-xs text-muted-foreground">
            <span>
              <Activity className="mr-1 inline h-3 w-3" />
              Moving {movePct.toFixed(1)}%
            </span>
            <span>
              Resting {restPct.toFixed(1)}%
            </span>
          </div>
          <div className="flex h-2 overflow-hidden rounded-full bg-muted">
            <div
              className="bg-primary"
              style={{ width: `${movePct}%` }}
              title={`Moving: ${fmt(Math.round(totals.workoutMinutes))} min across ${data.days.length} days`}
            />
            <div
              className="bg-emerald-500/60"
              style={{ width: `${restPct}%` }}
              title="Waking time not in a logged workout"
            />
          </div>
        </div>
      </div>
    </ChartCard>
  );
}

interface MiniStatProps {
  label: string;
  value: string;
  icon: typeof Activity;
  hint?: string;
}

function MiniStat({ label, value, icon: Icon, hint }: MiniStatProps) {
  return (
    <Card className="bg-muted/20">
      <CardContent className="p-3">
        <div className="flex items-center gap-1.5 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
          <Icon className="h-3 w-3" />
          {label}
        </div>
        <div
          className="mt-1 truncate text-lg font-semibold tabular-nums"
          title={value}
        >
          {value}
        </div>
        {hint && (
          <div className="truncate text-[11px] text-muted-foreground" title={hint}>
            {hint}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function fmt(n: number): string {
  if (n < 1000) return n.toLocaleString();
  if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}k`;
  return `${(n / 1_000_000).toFixed(n < 10_000_000 ? 1 : 0)}M`;
}

function formatShortDay(iso: string): string {
  const [, m, d] = iso.split("-");
  if (!m || !d) return iso;
  return `${Number(m)}/${Number(d)}`;
}
