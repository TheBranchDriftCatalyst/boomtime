import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  Bike,
  Dumbbell,
  Flame,
  Footprints,
  HeartPulse,
  Mountain,
  Moon,
  PersonStanding,
  Zap,
} from "lucide-react";
import { PageToolbar } from "@/components/toolbar/PageToolbar";
import { DateRangePicker } from "@/components/toolbar/DateRangePicker";
import { ChartCard } from "@/components/ChartCard";
import { QueryGate } from "@/components/QueryGate";
import { Card, CardContent } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { ColumnChart } from "@/viz/charts/ColumnChart";
import { useTimeRange } from "@/hooks/useTimeRange";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import type {
  HealthActivityDay,
  WorkoutEvent,
  WorkoutLabelSummary,
  WorkoutListPayload,
} from "@/types/api";

export function Wellness() {
  const tr = useTimeRange();
  const activityQ = useQuery({
    queryKey: qk.healthActivity(tr.startISO, tr.endISO),
    queryFn: () =>
      api.getHealthActivity({ start: tr.startISO, end: tr.endISO }),
  });
  // Second feed drives the events breakdown — per-workout rows plus per-label
  // aggregates. Kept as a separate query so users on very long ranges without
  // per-event needs don't pay for the event scan.
  const workoutsQ = useQuery({
    queryKey: qk.workoutList(tr.startISO, tr.endISO),
    queryFn: () =>
      api.getWorkoutList({ start: tr.startISO, end: tr.endISO }),
  });

  const dates = useMemo(
    () => activityQ.data?.days.map((d) => d.day) ?? [],
    [activityQ.data],
  );
  // ColumnChart formats via secondsToHms — convert workout minutes so the
  // tooltip reads "45m" instead of raw seconds. Non-time metrics (kcal, steps)
  // render in the daily table instead of ColumnChart to avoid HH:MM formatting.
  const workoutSeconds = useMemo(
    () => activityQ.data?.days.map((d) => Math.round(d.workoutMinutes * 60)) ?? [],
    [activityQ.data],
  );

  return (
    <div className="flex flex-col gap-6">
      <PageToolbar title="Wellness">
        <DateRangePicker
          numDays={tr.numDays}
          onPreset={tr.setDaysFromToday}
          onRange={tr.setRange}
        />
      </PageToolbar>

      <QueryGate
        query={activityQ}
        errorMessage="Failed to load health data."
      >
        {(data) => {
          if (!data.hasData) return <EmptyState />;

          const t = data.totals;
          return (
            <>
              <SummaryStrip totals={t} dayCount={data.days.length} />

              <ChartCard title="Workout minutes per day">
                <ColumnChart
                  dates={dates}
                  values={workoutSeconds}
                  seriesName="Workout"
                  height={220}
                />
              </ChartCard>

              {/* Event-level breakdown by user-annotated label. Two-tier:
                  first a grid of per-label totals ("all Morning Runs"),
                  then a flat list of individual workout events. */}
              <LabelBreakdown data={workoutsQ.data} />
              <EventsList data={workoutsQ.data} />

              <ChartCard title="Daily detail">
                <DailyTable days={data.days} />
              </ChartCard>
            </>
          );
        }}
      </QueryGate>
    </div>
  );
}

function EmptyState() {
  return (
    <Card>
      <CardContent className="flex flex-col items-center gap-3 py-10 text-center">
        <HeartPulse className="h-8 w-8 text-muted-foreground" />
        <div className="max-w-md text-sm text-muted-foreground">
          No health data in this range. Pair the BoomtimeWatch companion app
          (extensions/boomtime-watch) with this server to start syncing
          workouts, heart rate, steps, and sleep from your Apple Watch.
        </div>
      </CardContent>
    </Card>
  );
}

// Icon picked from the RAW HKWorkoutActivityType name, not the user label.
// Keeps the visual anchor stable when a user renames "running" to
// "Marathon Training" — the running icon still shows next to the label.
function iconForKind(kind: string): typeof Activity {
  switch (kind) {
    case "running":
      return PersonStanding;
    case "cycling":
      return Bike;
    case "strength":
    case "functional_strength":
      return Dumbbell;
    case "hiking":
      return Mountain;
    case "hiit":
      return Zap;
    case "yoga":
    case "pilates":
    case "core":
      return Activity;
    default:
      return Activity;
  }
}

interface LabelBreakdownProps {
  data: WorkoutListPayload | undefined;
}

function LabelBreakdown({ data }: LabelBreakdownProps) {
  if (!data || !data.hasData || data.byLabel.length === 0) return null;
  return (
    <ChartCard title="Breakdown by label">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
        {data.byLabel.map((s) => (
          <LabelTile key={s.label} summary={s} />
        ))}
      </div>
    </ChartCard>
  );
}

interface LabelTileProps {
  summary: WorkoutLabelSummary;
}

function LabelTile({ summary }: LabelTileProps) {
  const Icon = iconForKind(summary.kind);
  const avgMin = summary.count > 0 ? summary.totalMin / summary.count : 0;
  return (
    <Card className="bg-muted/20">
      <CardContent className="p-3">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2 text-sm font-semibold">
            <Icon className="h-4 w-4 text-muted-foreground" />
            <span className="truncate" title={summary.label}>
              {summary.label}
            </span>
          </div>
          <span className="text-[10px] uppercase text-muted-foreground">
            {summary.kind || "workout"}
          </span>
        </div>
        <div className="mt-2 grid grid-cols-3 gap-2 text-xs">
          <TileMetric label="Sessions" value={fmt(summary.count)} />
          <TileMetric
            label="Total min"
            value={Math.round(summary.totalMin).toLocaleString()}
          />
          <TileMetric
            label="Avg min"
            value={avgMin > 0 ? avgMin.toFixed(0) : "—"}
          />
          <TileMetric
            label="Total kcal"
            value={
              summary.totalKcal > 0
                ? fmt(Math.round(summary.totalKcal))
                : "—"
            }
          />
          <TileMetric
            label="Avg HR"
            value={summary.avgHR > 0 ? `${Math.round(summary.avgHR)}` : "—"}
          />
        </div>
      </CardContent>
    </Card>
  );
}

interface TileMetricProps {
  label: string;
  value: string;
}

function TileMetric({ label, value }: TileMetricProps) {
  return (
    <div>
      <div className="text-[9px] uppercase tracking-wide text-muted-foreground">
        {label}
      </div>
      <div className="text-sm font-semibold tabular-nums">{value}</div>
    </div>
  );
}

interface EventsListProps {
  data: WorkoutListPayload | undefined;
}

function EventsList({ data }: EventsListProps) {
  if (!data || !data.hasData || data.events.length === 0) return null;
  return (
    <ChartCard title="Recent workouts">
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b text-left text-xs uppercase tracking-wide text-muted-foreground">
              <th className="py-2 pr-3 font-medium">Start</th>
              <th className="py-2 pr-3 font-medium">Label</th>
              <th className="py-2 pr-3 font-medium">Kind</th>
              <th className="py-2 pr-3 text-right font-medium">Min</th>
              <th className="py-2 pr-3 text-right font-medium">kcal</th>
              <th className="py-2 pr-3 text-right font-medium">Avg HR</th>
              <th className="py-2 text-right font-medium">Dist</th>
            </tr>
          </thead>
          <tbody className="divide-y">
            {data.events.map((e) => (
              <EventRow key={e.id} event={e} />
            ))}
          </tbody>
        </table>
      </div>
    </ChartCard>
  );
}

interface EventRowProps {
  event: WorkoutEvent;
}

function EventRow({ event: e }: EventRowProps) {
  const Icon = iconForKind(e.kind);
  const min = Math.round(e.durationS / 60);
  const distKm = e.distanceM != null ? e.distanceM / 1000 : null;
  return (
    <tr className="hover:bg-muted/30">
      <td className="py-2 pr-3 tabular-nums" title={new Date(e.start * 1000).toISOString()}>
        {formatDayTime(e.start)}
      </td>
      <td className="py-2 pr-3">
        <div className="flex items-center gap-1.5">
          <Icon className="h-3.5 w-3.5 text-muted-foreground" />
          <span className="truncate font-medium" title={e.label}>
            {e.label || "—"}
          </span>
        </div>
      </td>
      <td className="py-2 pr-3 text-xs text-muted-foreground">{e.kind || "—"}</td>
      <td className="py-2 pr-3 text-right tabular-nums">{min}</td>
      <td className="py-2 pr-3 text-right tabular-nums">
        {e.kcal != null ? Math.round(e.kcal) : "—"}
      </td>
      <td className="py-2 pr-3 text-right tabular-nums">
        {e.avgHR != null && e.avgHR > 0 ? e.avgHR : "—"}
      </td>
      <td className="py-2 text-right tabular-nums">
        {distKm != null ? `${distKm.toFixed(2)} km` : "—"}
      </td>
    </tr>
  );
}

interface SummaryStripProps {
  totals: HealthActivityDay;
  dayCount: number;
}

// Mirrors WellnessCard's five-tile strip so the two surfaces (Overview card +
// dedicated page) feel like the same feature.
function SummaryStrip({ totals, dayCount }: SummaryStripProps) {
  const stepsPerDay = Math.round(totals.steps / Math.max(dayCount, 1));
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-5">
      <MiniStat
        label="Workouts"
        value={fmt(totals.workouts)}
        icon={Activity}
        hint={`over ${dayCount} day${dayCount === 1 ? "" : "s"}`}
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
        hint={`~${fmt(stepsPerDay)} / day`}
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
            ? `${(totals.sleepMinutes / 60 / Math.max(dayCount, 1)).toFixed(1)} h`
            : "—"
        }
        icon={Moon}
        hint={
          totals.hrvMs > 0 ? `HRV ~${Math.round(totals.hrvMs)} ms` : "avg / night"
        }
      />
    </div>
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

interface DailyTableProps {
  days: HealthActivityDay[];
}

function DailyTable({ days }: DailyTableProps) {
  const rows = useMemo(() => [...days].reverse(), [days]);
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b text-left text-xs uppercase tracking-wide text-muted-foreground">
            <th className="py-2 pr-3 font-medium">Day</th>
            <th className="py-2 pr-3 text-right font-medium">Workouts</th>
            <th className="py-2 pr-3 text-right font-medium">Min</th>
            <th className="py-2 pr-3 text-right font-medium">kcal</th>
            <th className="py-2 pr-3 text-right font-medium">Steps</th>
            <th className="py-2 pr-3 text-right font-medium">Avg HR</th>
            <th className="py-2 pr-3 text-right font-medium">Rest HR</th>
            <th className="py-2 pr-3 text-right font-medium">Sleep</th>
            <th className="py-2 text-right font-medium">HRV ms</th>
          </tr>
        </thead>
        <tbody className="divide-y">
          {rows.map((d) => (
            <tr key={d.day} className="hover:bg-muted/30">
              <td className="py-2 pr-3 font-medium tabular-nums">{d.day}</td>
              <td className="py-2 pr-3 text-right tabular-nums">
                {d.workouts || "—"}
              </td>
              <td className="py-2 pr-3 text-right tabular-nums">
                {d.workoutMinutes > 0 ? Math.round(d.workoutMinutes) : "—"}
              </td>
              <td className="py-2 pr-3 text-right tabular-nums">
                {d.activeKcal > 0 ? Math.round(d.activeKcal) : "—"}
              </td>
              <td className="py-2 pr-3 text-right tabular-nums">
                {d.steps > 0 ? fmt(d.steps) : "—"}
              </td>
              <td className="py-2 pr-3 text-right tabular-nums">
                {d.avgHR > 0 ? Math.round(d.avgHR) : "—"}
              </td>
              <td className="py-2 pr-3 text-right tabular-nums">
                {d.restingHR > 0 ? Math.round(d.restingHR) : "—"}
              </td>
              <td className="py-2 pr-3 text-right tabular-nums">
                {d.sleepMinutes > 0
                  ? `${(d.sleepMinutes / 60).toFixed(1)}h`
                  : "—"}
              </td>
              <td className="py-2 text-right tabular-nums">
                {d.hrvMs > 0 ? Math.round(d.hrvMs) : "—"}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function fmt(n: number): string {
  if (n < 1000) return n.toLocaleString();
  if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}k`;
  return `${(n / 1_000_000).toFixed(n < 10_000_000 ? 1 : 0)}M`;
}

function formatDayTime(unixSec: number): string {
  const d = new Date(unixSec * 1000);
  const day = `${d.getMonth() + 1}/${d.getDate()}`;
  const h = d.getHours();
  const m = d.getMinutes();
  return `${day} ${h}:${m.toString().padStart(2, "0")}`;
}
