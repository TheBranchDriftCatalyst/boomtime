import Chart from "react-apexcharts";
import type { ApexOptions } from "apexcharts";
import type { HeatmapChartProps } from "@/components/charts/types";
import { baseChart, truncateLabel } from "@/components/charts/base";
import { mergeApexTheme, useApexTheme } from "@/theme/apexTheme";
import { CHART_COLORS, NO_DATA } from "@/lib/config";
import { secondsToHms } from "@/lib/utils";
import type { ResourceStats } from "@/types/api";

/** Activity-per-resource heatmap (top N by total time). */
export function HeatmapChartApex({
  items,
  dates,
  topN = 7,
  height = 260,
}: HeatmapChartProps) {
  const apexTheme = useApexTheme();
  // Keep the backend's aggregated "Other (N more)" row (if present) and show it
  // alongside the top-N real resources.
  const isOther = (r: ResourceStats) => r.name.startsWith("Other (");
  const other = items.filter(isOther);
  const top = [
    ...items
      .filter((r) => !isOther(r))
      .sort((a, b) => b.totalSeconds - a.totalSeconds)
      .slice(0, topN),
    ...other,
  ];

  const series = top.map((r) => ({
    name: r.name,
    data: dates.map((d, i) => ({ x: d, y: r.totalDaily[i] ?? 0 })),
  }));

  const options: ApexOptions = {
    chart: { ...baseChart, type: "heatmap" },
    colors: CHART_COLORS,
    noData: NO_DATA,
    dataLabels: { enabled: false },
    xaxis: { type: "datetime" },
    tooltip: { y: { formatter: (val: number) => secondsToHms(val) } },
    yaxis: { labels: { formatter: truncateLabel } },
  };

  return (
    <Chart options={mergeApexTheme(options, apexTheme)} series={series} type="heatmap" height={height} />
  );
}
