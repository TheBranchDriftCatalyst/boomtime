import type { HeartbeatAxis } from "@/types/api";

/**
 * The single source of truth for the 9 axes that curation rules can target:
 * rename/remapping rules (RemappingForm), curation hide/suppress rules
 * (SUPPRESSIBLE_AXES in heartbeats/axes.ts is derived from this), and Space
 * membership rules (SpaceRuleForm). Excludes synthetic `day`/`isWrite`/`type`
 * and file-path `userAgent`.
 *
 * `entity` has no query-time remap in the dashboards, so a rename rule on it
 * only bites with "Apply at ingest" on (the ingest scrubber) — RemappingForm
 * surfaces that caveat when an ingest-only axis is selected.
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
  "entity",
];
