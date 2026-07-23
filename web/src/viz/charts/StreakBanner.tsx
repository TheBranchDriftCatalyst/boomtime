import { useMemo } from "react";
import * as d3 from "d3";
import { Card, CardContent } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { cssVar } from "@/viz/d3/useChartFrame";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";

interface StreakBannerProps {
  // RAW daily seconds (aligned to dates). The last entry is today (partial).
  dailyTotal: number[];
}

const ACTIVE_THRESHOLD = 300; // >= 5 min counts as an active day

function computeStreaks(daily: number[]) {
  const active = daily.map((s) => s >= ACTIVE_THRESHOLD);
  const totalDays = active.length;
  const activeDays = active.filter(Boolean).length;

  // Longest run of active days anywhere in the window.
  let longest = 0;
  let run = 0;
  for (const a of active) {
    run = a ? run + 1 : 0;
    if (run > longest) longest = run;
  }

  // Current streak: count back from YESTERDAY (exclude today, which is partial
  // and would misreport the streak). If yesterday is inactive, current is 0.
  let current = 0;
  for (let i = active.length - 2; i >= 0; i--) {
    if (active[i]) current++;
    else break;
  }

  return {
    current,
    longest,
    activeDays,
    totalDays,
    ratio: totalDays > 0 ? activeDays / totalDays : 0,
  };
}

export function StreakBanner({ dailyTotal }: StreakBannerProps) {
  const stats = useMemo(() => computeStreaks(dailyTotal), [dailyTotal]);
  const spark = useMemo(() => dailyTotal.slice(-30), [dailyTotal]);

  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
      <StreakStat label="Current streak" value={`${stats.current}d`} hint="excl. today" />
      <StreakStat label="Longest streak" value={`${stats.longest}d`} />
      <StreakStat
        label="Active days"
        value={`${Math.round(stats.ratio * 100)}%`}
        hint={`${stats.activeDays}/${stats.totalDays} days`}
      />
      <Card>
        <CardContent className="flex flex-col justify-center gap-1 p-5">
          <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
            Last 30 days
          </p>
          <Sparkline values={spark} />
        </CardContent>
      </Card>
    </div>
  );
}

function StreakStat({
  label,
  value,
  hint,
}: {
  label: string;
  value: string;
  hint?: string;
}) {
  return (
    <Card>
      <CardContent className="p-5">
        <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
          {label}
        </p>
        <p className="mt-1 text-2xl font-bold">{value}</p>
        {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
      </CardContent>
    </Card>
  );
}

function Sparkline({ values }: { values: number[] }) {
  const height = 36;

  const surface = useD3Surface(
    { height },
    ({ svg, width }) => {
      if (values.length === 0) return;

      const primary = cssVar("--primary");
      const x = d3
        .scaleLinear()
        .domain([0, Math.max(1, values.length - 1)])
        .range([1, width - 1]);
      const yMax = d3.max(values) ?? 0;
      const y = d3.scaleLinear().domain([0, yMax || 1]).range([height - 2, 2]);

      const area = d3
        .area<number>()
        .x((_d, i) => x(i))
        .y0(height)
        .y1((d) => y(d))
        .curve(d3.curveMonotoneX);
      const line = d3
        .line<number>()
        .x((_d, i) => x(i))
        .y((d) => y(d))
        .curve(d3.curveMonotoneX);

      svg
        .append("path")
        .datum(values)
        .attr("d", area)
        .attr("fill", primary)
        .attr("fill-opacity", 0.15);
      svg
        .append("path")
        .datum(values)
        .attr("d", line)
        .attr("fill", "none")
        .attr("stroke", primary)
        .attr("stroke-width", 1.5);
    },
    [values],
  );

  return <ChartSurface surface={surface} />;
}
