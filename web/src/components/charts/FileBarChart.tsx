import { useRenderer } from "@/viz/rendererContext";
import { FileBarChartApex } from "@/components/charts/FileBarChartApex";
import { FileBarChartD3 } from "@/components/charts/FileBarChartD3";
import type { FileBarChartProps } from "@/components/charts/types";

/** Renderer switcher — same export name + props as before (drop-in). */
export function FileBarChart(props: FileBarChartProps) {
  const { renderer } = useRenderer();
  return renderer === "d3" ? <FileBarChartD3 {...props} /> : <FileBarChartApex {...props} />;
}
