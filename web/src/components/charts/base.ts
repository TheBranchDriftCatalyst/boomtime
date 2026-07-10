import type { ApexOptions } from "apexcharts";
import { CHART_COLORS, NO_DATA } from "@/lib/config";
import { secondsToHms, truncate } from "@/lib/utils";

// Shared bits reused across every chart so they inherit the modern theme.
export const baseChart: ApexOptions["chart"] = {
  toolbar: { show: false },
  animations: { enabled: true },
  background: "transparent",
  fontFamily: "inherit",
};

export const commonOptions: Partial<ApexOptions> = {
  colors: CHART_COLORS,
  noData: NO_DATA,
  chart: baseChart,
};

export const secondsTooltip = {
  y: {
    formatter: (val: number) => secondsToHms(val),
  },
};

export const hoursYAxisLabels = {
  formatter: (val: number) => (val / 3600).toFixed(1),
};

export const truncateLabel = (val: string) =>
  typeof val === "string" ? truncate(val, 12) : val;
