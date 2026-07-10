import { useRenderer } from "@/viz/rendererContext";
import { ColumnChartApex } from "@/components/charts/ColumnChartApex";
import { ColumnChartD3 } from "@/components/charts/ColumnChartD3";
import type { ColumnChartProps } from "@/components/charts/types";

/** Renderer switcher — same export name + props as before (drop-in). */
export function ColumnChart(props: ColumnChartProps) {
  const { renderer } = useRenderer();
  return renderer === "d3" ? <ColumnChartD3 {...props} /> : <ColumnChartApex {...props} />;
}
