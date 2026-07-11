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

// Axes whose curation "hide" rules the backend actually excludes from the
// dashboards. The Explorer's Suppress toggle is only offered for these — a hide
// rule on any other axis would be a no-op against the dashboards.
//
// Backend coverage (LoadHiddenSets / exclusionPredicate) spans all 8 of these:
// every aggregate dashboard (raw + rollup stats, projects list, leaderboards,
// category/punchcard/sessions/momentum) excludes a suppressed value; the rollup
// falls back to a raw gap_seconds scan for plugin/branch/category. Verified by
// internal/db/suppression_test.go (TestSuppressedValuesExcludedFromAggregations).
// `day`, `type`, `entity`, and `userAgent` are never suppressible.
export const SUPPRESSIBLE_AXES: ReadonlySet<HeartbeatAxis> = new Set([
  "project",
  "language",
  "editor",
  "plugin",
  "machine",
  "platform",
  "branch",
  "category",
]);

export function isSuppressibleAxis(axis: HeartbeatAxis): boolean {
  return SUPPRESSIBLE_AXES.has(axis);
}
