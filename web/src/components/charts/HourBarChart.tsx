import { useRenderer } from "@/viz/rendererContext";
import { HourBarChartApex } from "@/components/charts/HourBarChartApex";
import { HourBarChartD3 } from "@/components/charts/HourBarChartD3";
import type { HourBarChartProps } from "@/components/charts/types";

/** Renderer switcher — same export name + props as before (drop-in). */
export function HourBarChart(props: HourBarChartProps) {
  const { renderer } = useRenderer();
  return renderer === "d3" ? <HourBarChartD3 {...props} /> : <HourBarChartApex {...props} />;
}
