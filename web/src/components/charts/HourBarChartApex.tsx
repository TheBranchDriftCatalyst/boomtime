import Chart from "react-apexcharts";
import type { ApexOptions } from "apexcharts";
import type { HourBarChartProps } from "@/components/charts/types";
import {
  baseChart,
  hoursYAxisLabels,
  secondsTooltip,
} from "@/components/charts/base";
import { mergeApexTheme, useApexTheme } from "@/theme/apexTheme";
import { CHART_COLORS, NO_DATA } from "@/lib/config";
import { addTimeOffset } from "@/lib/utils";

/** Activity-per-hour-of-day bar chart (Projects page). */
export function HourBarChartApex({ hour, height = 320 }: HourBarChartProps) {
  const apexTheme = useApexTheme();
  const data = Array(24).fill(0);
  for (const v of hour) {
    data[addTimeOffset(v.name)] = v.totalSeconds;
  }

  const options: ApexOptions = {
    chart: { ...baseChart, type: "bar" },
    colors: CHART_COLORS,
    noData: NO_DATA,
    tooltip: secondsTooltip,
    dataLabels: { enabled: false },
    plotOptions: { bar: { columnWidth: "45%", borderRadius: 3 } },
    yaxis: { title: { text: "Hours" }, labels: hoursYAxisLabels },
    xaxis: { categories: Array.from({ length: 24 }, (_, i) => i) },
    grid: { borderColor: "var(--border)", strokeDashArray: 4 },
  };

  return (
    <Chart
      options={mergeApexTheme(options, apexTheme)}
      series={[{ data, name: "Activity" }]}
      type="bar"
      height={height}
    />
  );
}
