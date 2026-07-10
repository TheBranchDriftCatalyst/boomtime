// Shared prop contracts for every chart. Every chart is a single D3-rendered
// component; props live here so call sites stay stable.
import type { ResourceStats, TimelinePayload } from "@/types/api";

// One stacked series (segment) of a stacked column chart. `values` is aligned
// to the chart's `dates`; `seconds` per bucket.
export interface ColumnSeries {
  name: string;
  values: number[];
  color: string;
}

export interface ColumnChartProps {
  // Parallel arrays: ISO dates and per-day totals in seconds.
  dates: string[];
  values?: number[];
  seriesName?: string;
  height?: number;
  // When provided, render a stacked column chart (one stacked bar per date,
  // one segment per series) instead of the single-series `values` bars. Each
  // series carries its own color so callers can share a palette across charts.
  series?: ColumnSeries[];
}

export interface PieChartProps {
  items: ResourceStats[];
  height?: number;
}

export interface HeatmapChartProps {
  // Top-N resources (projects or languages).
  items: ResourceStats[];
  dates: string[];
  topN?: number;
  height?: number;
}

export interface RadarChartProps {
  // weekDay resources: name is the weekday index (0-6).
  weekDay: ResourceStats[];
  height?: number;
}

export interface HourBarChartProps {
  // hour resources: name is the hour-of-day (0-23) in UTC.
  hour: ResourceStats[];
  height?: number;
}

export interface FileBarChartProps {
  files: ResourceStats[];
  height?: number;
}

export interface TimelineChartProps {
  timeline: TimelinePayload | undefined;
  height?: number;
}
