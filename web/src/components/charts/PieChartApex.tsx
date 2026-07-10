import Chart from "react-apexcharts";
import type { ApexOptions } from "apexcharts";
import type { PieChartProps } from "@/components/charts/types";
import { baseChart } from "@/components/charts/base";
import { mergeApexTheme, useApexTheme } from "@/theme/apexTheme";
import { CHART_COLORS, NO_DATA } from "@/lib/config";
import { secondsToHms } from "@/lib/utils";

/**
 * Breakdown pie (projects / languages). The backend already caps each dimension
 * to the top resources + one aggregated "Other (N more)" slice, so this just
 * filters sub-minute noise and renders what it's given.
 */
export function PieChartApex({ items, height = 320 }: PieChartProps) {
  const apexTheme = useApexTheme();
  const filtered = items.filter((v) => v.totalSeconds >= 60);
  const series = filtered.map((v) => v.totalSeconds);
  const labels = filtered.map((v) => v.name);

  const options: ApexOptions = {
    chart: { ...baseChart, type: "pie" },
    colors: CHART_COLORS,
    noData: NO_DATA,
    labels,
    legend: { show: false },
    stroke: { width: 1, colors: ["var(--card)"] },
    tooltip: {
      y: { formatter: (val: number) => secondsToHms(val) },
    },
    dataLabels: { enabled: true },
  };

  if (series.length === 0) {
    return <EmptyChart height={height} />;
  }

  return (
    <Chart options={mergeApexTheme(options, apexTheme)} series={series} type="pie" height={height} />
  );
}

function EmptyChart({ height }: { height: number }) {
  return (
    <div
      className="flex items-center justify-center text-sm text-muted-foreground"
      style={{ height }}
    >
      No data available
    </div>
  );
}
