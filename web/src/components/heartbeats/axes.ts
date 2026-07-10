import type { HeartbeatAxis } from "@/types/api";

export interface AxisMeta {
  axis: HeartbeatAxis;
  label: string;
  // Section shown in the axis picker.
  section: "General" | "Source";
}

// Ordered list of every groupable axis with friendly labels. The Source group
// (editor/plugin/machine/platform/userAgent) is visually clustered together.
export const AXES: AxisMeta[] = [
  { axis: "day", label: "Day", section: "General" },
  { axis: "project", label: "Project", section: "General" },
  { axis: "language", label: "Language", section: "General" },
  { axis: "branch", label: "Branch", section: "General" },
  { axis: "category", label: "Category", section: "General" },
  { axis: "type", label: "Type", section: "General" },
  { axis: "entity", label: "Entity", section: "General" },
  { axis: "isWrite", label: "Is write", section: "General" },
  { axis: "editor", label: "Editor", section: "Source" },
  { axis: "plugin", label: "Plugin", section: "Source" },
  { axis: "machine", label: "Machine", section: "Source" },
  { axis: "platform", label: "Platform", section: "Source" },
  { axis: "userAgent", label: "User agent", section: "Source" },
];

const LABEL_BY_AXIS = new Map(AXES.map((a) => [a.axis, a.label]));

export function axisLabel(axis: HeartbeatAxis): string {
  return LABEL_BY_AXIS.get(axis) ?? axis;
}

export const DEFAULT_GROUP_BY: HeartbeatAxis[] = ["project", "day"];

export const LEAF_PAGE_SIZE = 50;
