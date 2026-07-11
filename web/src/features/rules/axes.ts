import type { HeartbeatAxis } from "@/types/api";

/**
 * The single source of truth for the 8 axes that curation rules can target:
 * rename/remapping rules (RemappingForm), curation hide/suppress rules
 * (SUPPRESSIBLE_AXES in heartbeats/axes.ts is derived from this), and Space
 * membership rules (SpaceRuleForm). Excludes synthetic `day`/`isWrite`/`type`
 * and file-path `entity`/`userAgent`.
 */
export const CURATABLE_AXES: readonly HeartbeatAxis[] = [
  "project",
  "language",
  "editor",
  "plugin",
  "machine",
  "platform",
  "branch",
  "category",
];
