import { useRenderer } from "@/viz/rendererContext";
import { RadarChartApex } from "@/components/charts/RadarChartApex";
import { RadarChartD3 } from "@/components/charts/RadarChartD3";
import type { RadarChartProps } from "@/components/charts/types";

/** Renderer switcher — same export name + props as before (drop-in). */
export function RadarChart(props: RadarChartProps) {
  const { renderer } = useRenderer();
  return renderer === "d3" ? <RadarChartD3 {...props} /> : <RadarChartApex {...props} />;
}
