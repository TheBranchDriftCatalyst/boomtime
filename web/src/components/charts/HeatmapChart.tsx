import { useRenderer } from "@/viz/rendererContext";
import { HeatmapChartApex } from "@/components/charts/HeatmapChartApex";
import { HeatmapChartD3 } from "@/components/charts/HeatmapChartD3";
import type { HeatmapChartProps } from "@/components/charts/types";

/** Renderer switcher — same export name + props as before (drop-in). */
export function HeatmapChart(props: HeatmapChartProps) {
  const { renderer } = useRenderer();
  return renderer === "d3" ? <HeatmapChartD3 {...props} /> : <HeatmapChartApex {...props} />;
}
