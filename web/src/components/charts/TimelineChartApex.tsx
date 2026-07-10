import Chart from "react-apexcharts";
import type { ApexOptions } from "apexcharts";
import type { TimelineChartProps } from "@/components/charts/types";
import { baseChart, truncateLabel } from "@/components/charts/base";
import { mergeApexTheme, useApexTheme } from "@/theme/apexTheme";
import { CHART_COLORS, NO_DATA } from "@/lib/config";

/** Recent timeline as a rangeBar (per-language activity segments). */
export function TimelineChartApex({ timeline, height = 350 }: TimelineChartProps) {
  const apexTheme = useApexTheme();
  const langs = timeline?.langs ?? {};
  const series = Object.keys(langs).map((lang) => ({
    name: lang,
    data: langs[lang].map((v) => ({
      x: v.name,
      y: [new Date(v.rangeStart).getTime(), new Date(v.rangeEnd).getTime()],
    })),
  }));

  const options: ApexOptions = {
    chart: { ...baseChart, type: "rangeBar" },
    colors: CHART_COLORS,
    noData: NO_DATA,
    plotOptions: { bar: { horizontal: true } },
    grid: {
      show: true,
      borderColor: "var(--border)",
      xaxis: { lines: { show: true } },
    },
    xaxis: { type: "datetime", labels: { datetimeUTC: false } },
    yaxis: { labels: { formatter: truncateLabel } },
    tooltip: { x: { show: true, format: "d MMM, HH:mm" } },
    legend: { position: "top", horizontalAlign: "left" },
  };

  return (
    <Chart options={mergeApexTheme(options, apexTheme)} series={series} type="rangeBar" height={height} />
  );
}
