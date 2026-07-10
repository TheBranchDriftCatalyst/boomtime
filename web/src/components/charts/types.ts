// Shared prop contracts for every chart. Each chart has a switcher, an Apex
// implementation, and a D3 implementation — all three use the SAME props from
// here so the switcher is a pure drop-in and call sites never change.
import type { ResourceStats, TimelinePayload } from "@/types/api";

export interface ColumnChartProps {
  // Parallel arrays: ISO dates and per-day totals in seconds.
  dates: string[];
  values: number[];
  seriesName?: string;
  height?: number;
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
