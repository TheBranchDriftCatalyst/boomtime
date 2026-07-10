import Chart from "react-apexcharts";
import type { ApexOptions } from "apexcharts";
import type { FileBarChartProps } from "@/components/charts/types";
import { baseChart, hoursYAxisLabels } from "@/components/charts/base";
import { mergeApexTheme, useApexTheme } from "@/theme/apexTheme";
import { CHART_COLORS, NO_DATA } from "@/lib/config";
import { secondsToHms } from "@/lib/utils";

// Shorten a file path to "<parent>/<file>" for the data label.
function shortLabel(val: string): string {
  if (typeof val !== "string") return val;
  const parts = val.split("/").filter(Boolean);
  const filename = parts[parts.length - 1] ?? val;
  const parent = parts[parts.length - 2];
  return parent ? `${parent}/${filename}` : filename;
}

/** Most-active files as a horizontal bar chart (top 10). */
export function FileBarChartApex({ files, height = 380 }: FileBarChartProps) {
  const apexTheme = useApexTheme();
  const top = [...files]
    // Drop the backend's aggregated "Other (N more)" bucket — this chart lists
    // specific files, not an aggregate.
    .filter((v) => v.totalSeconds / 3600 > 0 && !v.name.startsWith("Other ("))
    .sort((a, b) => b.totalSeconds - a.totalSeconds)
    .slice(0, 10);

  const data = top.map((v) => v.totalSeconds);
  const categories = top.map((v) => v.name);

  const options: ApexOptions = {
    chart: { ...baseChart, type: "bar" },
    colors: CHART_COLORS,
    noData: NO_DATA,
    tooltip: { y: { formatter: (val: number) => secondsToHms(val) } },
    plotOptions: {
      bar: {
        horizontal: true,
        distributed: true,
        borderRadius: 3,
        barHeight: "80%",
      },
    },
    dataLabels: {
      enabled: true,
      textAnchor: "start",
      offsetX: 0,
      style: { colors: ["#fff"], fontSize: "11px" },
      // A dark drop-shadow keeps the white basename legible even when a short
      // bar's label extends past the bar onto the plot background.
      dropShadow: {
        enabled: true,
        left: 0,
        top: 0,
        blur: 2,
        color: "#000",
        opacity: 0.85,
      },
      formatter: (_val, opt) => {
        const label = opt.w.globals.labels[opt.dataPointIndex];
        return shortLabel(label);
      },
    },
    yaxis: { show: false },
    legend: { show: false },
    xaxis: {
      title: { text: "Hours" },
      labels: hoursYAxisLabels,
      categories,
    },
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
