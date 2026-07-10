import { useRenderer } from "@/viz/rendererContext";
import { PieChartApex } from "@/components/charts/PieChartApex";
import { PieChartD3 } from "@/components/charts/PieChartD3";
import type { PieChartProps } from "@/components/charts/types";

/** Renderer switcher — same export name + props as before (drop-in). */
export function PieChart(props: PieChartProps) {
  const { renderer } = useRenderer();
  return renderer === "d3" ? <PieChartD3 {...props} /> : <PieChartApex {...props} />;
}
