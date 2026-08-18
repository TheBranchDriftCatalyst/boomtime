// Shared admin-registration seam (types).
//
// The admin TAB STRIP is registered here as pure metadata (id + label + route
// + group) — no tab-body imports, so registering the admin surface never pulls
// a component. The tab bodies stay lazy-loaded at the router boundary (App.tsx),
// mirroring the backend where each domain owns its admin surface via a
// Module.RegisterAdminRoutes seam.
import type { NavFlag } from "@/shared/nav/types";

/** Identity + presentation of a domain group in the admin tab strip. */
export interface AdminGroupMeta {
  /** stable id — a domain key ("catalystbooks", "boomtime") or "core". */
  id: string;
  /** group header shown in the strip; omit for an ungrouped/core cluster. */
  label?: string;
  /** sort key across groups (ascending). */
  order?: number;
}

/** One admin tab: strip metadata + the group it belongs to. */
export interface AdminTabDef {
  /** stable id. */
  id: string;
  label: string;
  /** route the NavLink points at (also drives active state). */
  to: string;
  /** the domain group this tab renders under. */
  group: AdminGroupMeta;
  /** optional public-config gate. */
  flag?: NavFlag;
  /** sort key within the group (ascending). */
  order?: number;
}

/** A resolved group: its meta plus the tabs registered into it. */
export interface AdminGroup extends AdminGroupMeta {
  tabs: AdminTabDef[];
}
