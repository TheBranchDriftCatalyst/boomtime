import type { HeartbeatAxis } from "@/types/api";

// Explorer-only constants. The shared axis metadata (AXES, axisLabel) lives in
// @/lib/axes since curation, rules, and spaces consume it too. The
// suppressible-axis rules live with the curation layer
// (@boomtime/features/curation/explorer/curationAxes).

export const DEFAULT_GROUP_BY: HeartbeatAxis[] = ["project", "day"];

export const LEAF_PAGE_SIZE = 50;
