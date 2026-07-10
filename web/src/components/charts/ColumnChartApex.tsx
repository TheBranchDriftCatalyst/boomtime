import Chart from "react-apexcharts";
import type { ApexOptions } from "apexcharts";
import type { ColumnChartProps } from "@/components/charts/types";
import {
  baseChart,
  hoursYAxisLabels,
  secondsTooltip,
} from "@/components/charts/base";
import { mergeApexTheme, useApexTheme } from "@/theme/apexTheme";
import { CHART_COLORS, NO_DATA } from "@/lib/config";

/** Daily activity as a column/bar chart (Overview + Projects "Total activity"). */
export function ColumnChartApex({
  dates,
  values,
  seriesName = "Coding time",
  height = 320,
}: ColumnChartProps) {
  const apexTheme = useApexTheme();
  const data = dates.map((d, i) => ({ x: d, y: values[i] ?? 0 }));

  const options: ApexOptions = {
    chart: { ...baseChart, type: "bar" },
    colors: CHART_COLORS,
    noData: NO_DATA,
    xaxis: { type: "datetime" },
    yaxis: {
      title: { text: "Hours" },
      labels: hoursYAxisLabels,
    },
    tooltip: secondsTooltip,
    dataLabels: { enabled: false },
    plotOptions: {
      bar: { columnWidth: "45%", borderRadius: 4 },
    },
    grid: { borderColor: "var(--border)", strokeDashArray: 4 },
  };

  return (
    <Chart
      options={mergeApexTheme(options, apexTheme)}
      series={[{ name: seriesName, data }]}
      type="bar"
      height={height}
    />
  );
}
