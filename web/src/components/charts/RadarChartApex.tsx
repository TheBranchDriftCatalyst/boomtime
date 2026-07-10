import Chart from "react-apexcharts";
import type { ApexOptions } from "apexcharts";
import type { RadarChartProps } from "@/components/charts/types";
import { baseChart } from "@/components/charts/base";
import { mergeApexTheme, useApexTheme } from "@/theme/apexTheme";
import { CHART_COLORS, NO_DATA } from "@/lib/config";
import { secondsToHms } from "@/lib/utils";

const WEEKDAYS = [
  "Sunday",
  "Monday",
  "Tuesday",
  "Wednesday",
  "Thursday",
  "Friday",
  "Saturday",
];

/** Activity-per-weekday radar (Projects page). */
export function RadarChartApex({ weekDay, height = 320 }: RadarChartProps) {
  const apexTheme = useApexTheme();
  const data = Array(7).fill(0);
  for (const v of weekDay) {
    const idx = parseInt(v.name, 10);
    if (!Number.isNaN(idx) && idx >= 0 && idx < 7) data[idx] = v.totalSeconds;
  }

  const options: ApexOptions = {
    chart: { ...baseChart, type: "radar" },
    colors: CHART_COLORS,
    noData: NO_DATA,
    tooltip: { y: { formatter: (val: number) => secondsToHms(val) } },
    dataLabels: { enabled: false },
    xaxis: { categories: WEEKDAYS },
    yaxis: { show: false },
    plotOptions: {
      radar: {
        polygons: {
          strokeColors: "var(--border)",
          connectorColors: "var(--border)",
        },
      },
    },
  };

  return (
    <Chart
      options={mergeApexTheme(options, apexTheme)}
      series={[{ data, name: "Activity" }]}
      type="radar"
      height={height}
    />
  );
}
