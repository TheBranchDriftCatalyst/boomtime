import { useRenderer } from "@/viz/rendererContext";
import { TimelineChartApex } from "@/components/charts/TimelineChartApex";
import { TimelineChartD3 } from "@/components/charts/TimelineChartD3";
import type { TimelineChartProps } from "@/components/charts/types";

/** Renderer switcher — same export name + props as before (drop-in). */
export function TimelineChart(props: TimelineChartProps) {
  const { renderer } = useRenderer();
  return renderer === "d3" ? <TimelineChartD3 {...props} /> : <TimelineChartApex {...props} />;
}
