import { useMemo } from "react";
import * as d3 from "d3";
import { Card, CardContent } from "@/components/ui/card";
import { secondsToHms } from "@/lib/utils";
import { cssVar } from "@/viz/d3/useChartFrame";
import { useD3Surface } from "@/viz/d3/useD3Surface";
import { ChartSurface } from "@/viz/d3/ChartSurface";
import { tooltipHtml } from "@/viz/d3/tooltip";
import { formatDay, styleAxis, thinnedDateTicks } from "@/viz/d3/axes";
import { colorAt } from "@/viz/d3/color";
import { EmptyChart } from "@/viz/d3/EmptyChart";
import { bucketGroups, bucketSum } from "@/viz/bucket";
import type { SessionsPayload } from "@/types/api";

interface DeepWorkSessionsProps {
  data: SessionsPayload | undefined;
  height?: number;
}

/**
 * Deep-work focus sessions: summary stat cards (count / avg / longest) + a
 * session-length histogram (bar) + a small bucketed daily-sessions strip.
 */
export function DeepWorkSessions({ data, height = 220 }: DeepWorkSessionsProps) {
  if (!data || data.summary.count === 0) {
    return <EmptyChart height={height + 90} />;
  }
  const { summary, histogram, daily } = data;

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-4 sm:grid-cols-3">
        <SessionStat label="Sessions" value={String(summary.count)} />
        <SessionStat label="Avg length" value={secondsToHms(summary.avgSeconds)} />
        <SessionStat label="Longest" value={secondsToHms(summary.maxSeconds)} />
      </div>
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <div>
          <p className="mb-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">
            Session length distribution
          </p>
          <Histogram bins={histogram} height={height} />
        </div>
        <div>
          <p className="mb-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">
            Sessions over time
          </p>
          <DailyStrip daily={daily} height={height} />
        </div>
      </div>
    </div>
  );
}

function SessionStat({ label, value }: { label: string; value: string }) {
  return (
    <Card>
      <CardContent className="p-4">
        <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
          {label}
        </p>
        <p className="mt-1 text-xl font-bold">{value}</p>
      </CardContent>
    </Card>
  );
}

function Histogram({
  bins,
  height,
}: {
  bins: SessionsPayload["histogram"];
  height: number;
}) {
  const surface = useD3Surface(
    { height, margin: { top: 8, right: 8, bottom: 34, left: 30 } },
    ({ g, innerW, innerH, showTip, hideTip }) => {
      if (bins.length === 0) return;

      const fg = cssVar("--muted-foreground");

      const x = d3.scaleBand<string>().domain(bins.map((b) => b.label)).range([0, innerW]).padding(0.25);
      const yMax = d3.max(bins, (b) => b.count) ?? 0;
      const y = d3.scaleLinear().domain([0, yMax || 1]).nice().range([innerH, 0]);

      styleAxis(
        g.append("g").call(d3.axisLeft(y).ticks(4).tickFormat((v) => String(v))),
        { fg },
        { fontSize: "10px" },
      );

      styleAxis(
        g
          .append("g")
          .attr("transform", `translate(0,${innerH})`)
          .call(d3.axisBottom(x)),
        { fg },
        { fontSize: "9px" },
      )
        .selectAll("text")
        .attr("transform", "rotate(-30)")
        .style("text-anchor", "end");

      g.selectAll("rect.bar")
        .data(bins)
        .join("rect")
        .attr("class", "bar")
        .attr("x", (b) => x(b.label) ?? 0)
        .attr("width", x.bandwidth())
        .attr("y", (b) => y(b.count))
        .attr("height", (b) => innerH - y(b.count))
        .attr("rx", 2)
        .attr("fill", colorAt(3))
        .on("mousemove", (event, b) => {
          showTip(event, tooltipHtml(b.label, `${b.count} sessions`));
        })
        .on("mouseleave", hideTip);
    },
    [bins],
  );

  return <ChartSurface surface={surface} />;
}

function DailyStrip({
  daily,
  height,
}: {
  daily: SessionsPayload["daily"];
  height: number;
}) {
  // Bucket the daily session counts so long ranges stay bounded.
  const bucketed = useMemo(() => {
    const groups = bucketGroups(daily.length);
    const counts = bucketSum(groups, daily.map((d) => d.sessions));
    const labels = groups.map((gr) => daily[gr[0]]?.date ?? "");
    return counts.map((c, i) => ({ date: labels[i], sessions: c }));
  }, [daily]);

  const surface = useD3Surface(
    { height, margin: { top: 8, right: 8, bottom: 22, left: 24 } },
    ({ g, innerW, innerH, showTip, hideTip }) => {
      if (bucketed.length === 0) return;

      const fg = cssVar("--muted-foreground");

      const x = d3.scaleBand<number>().domain(d3.range(bucketed.length)).range([0, innerW]).padding(0.2);
      const yMax = d3.max(bucketed, (d) => d.sessions) ?? 0;
      const y = d3.scaleLinear().domain([0, yMax || 1]).nice().range([innerH, 0]);

      styleAxis(
        g.append("g").call(d3.axisLeft(y).ticks(3).tickFormat((v) => String(v))),
        { fg },
        { fontSize: "10px" },
      );

      styleAxis(
        g
          .append("g")
          .attr("transform", `translate(0,${innerH})`)
          .call(
            d3
              .axisBottom(x)
              .tickValues(thinnedDateTicks(d3.range(bucketed.length), 6))
              .tickFormat((i) => {
                const d = bucketed[Number(i)]?.date;
                return d ? formatDay(new Date(d)) : "";
              }),
          ),
        { fg },
        { fontSize: "10px" },
      );

      g.selectAll("rect.bar")
        .data(bucketed)
        .join("rect")
        .attr("class", "bar")
        .attr("x", (_d, i) => x(i) ?? 0)
        .attr("width", x.bandwidth())
        .attr("y", (d) => y(d.sessions))
        .attr("height", (d) => innerH - y(d.sessions))
        .attr("rx", 2)
        .attr("fill", colorAt(2))
        .on("mousemove", (event, d) => {
          showTip(
            event,
            tooltipHtml(
              d.date ? d3.timeFormat("%d %b %Y")(new Date(d.date)) : "",
              `${d.sessions} sessions`,
            ),
          );
        })
        .on("mouseleave", hideTip);
    },
    [bucketed],
  );

  return <ChartSurface surface={surface} />;
}
