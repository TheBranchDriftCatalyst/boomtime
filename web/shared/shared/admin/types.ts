// Shared admin-registration seam (types).
//
// The admin surface is registered as pure metadata — no tab-body imports — so
// registering it never pulls a component. Tab bodies stay lazy-loaded at the
// router boundary (App.tsx), mirroring the backend where each domain owns its
// admin surface via a Module.RegisterAdminRoutes seam.
//
// ── WHAT A DOMAIN CAN CUSTOMIZE (boom-9e9k) ─────────────────────────────────
// The division of labour is deliberate, and it's what keeps a domain from
// breaking the shell the way the old header tab strip did:
//
//   the SHELL owns  → where nav lives, how groups are presented, the page
//                     title row, the scroll contract, the width clamp
//   the DOMAIN owns → which tabs exist, their labels/icons/descriptions,
//                     their order, their content width CLASS (not a raw
//                     max-w), and the tab body itself
//
// Anything a domain needs that is DYNAMIC — an action button closing over the
// tab's own state, a live count — travels UP through `usePageActions()` rather
// than being hand-rolled into a bespoke header. See PageActionsSlot.tsx.
import type { ComponentType } from "react";

import type { SectionWidth } from "@shared/layout/SectionPage";
import type { NavFlag } from "@shared/shared/nav/types";

/**
 * Content width for a tab body — a NAMED SCALE ("default" | "wide" | "full")
 * rather than a free-form class, because every tab used to pick its own
 * `max-w-*` and the content column visibly jumped as you moved between them.
 *
 * The scale is defined by the layout that APPLIES it ({@link SectionWidth}) and
 * re-exported here so a domain reading this file sees the whole tab contract in
 * one place. Settings points at the same type — one vocabulary, two seams.
 */
export type AdminTabWidth = SectionWidth;

/** Identity + presentation of a domain group in the admin section rail. */
export interface AdminGroupMeta {
  /** stable id — a domain key ("catalystbooks", "boomtime") or "core". */
  id: string;
  /** group header shown in the rail; omit for an unlabeled (core) cluster. */
  label?: string;
  /** sort key across groups (ascending). */
  order?: number;
  /** optional glyph for the group header. */
  icon?: ComponentType<{ className?: string }>;
  /** one-line description of what this domain's admin surface covers. */
  description?: string;
  /** when set, the whole group hides unless this public-config flag is on. */
  flag?: NavFlag;
}

/** One admin tab: its identity, presentation, and the group it renders under. */
export interface AdminTabDef {
  /** stable id. */
  id: string;
  label: string;
  /** route the NavLink points at (also drives active state). For an
   *  `external` tab this is a plain URL instead. */
  to: string;
  /**
   * Id of the group this tab renders under — see {@link registerAdminGroup}.
   * A plain string rather than an inline meta object: the group's label/order/
   * icon used to be re-declared on every tab that belonged to it, so five tabs
   * meant five copies of the same three fields and any drift silently won
   * (last registration set the label).
   */
  group: string;
  /** optional leading glyph in the rail. */
  icon?: ComponentType<{ className?: string }>;
  /**
   * Supporting copy under the tab's title. The section shell renders it, so a
   * tab body never hand-rolls its own title/description block.
   */
  description?: string;
  /** content width class for the body (default: "default"). */
  width?: AdminTabWidth;
  /** when true, `to` addresses something OUTSIDE the SPA (a server-rendered
   *  page like the Swagger UI at /api/docs/). Rendered as an <a target="_blank">
   *  rather than a NavLink: the router never sees it, so it can't 404 on a path
   *  the SPA doesn't own, and it is never the active tab. */
  external?: boolean;
  /** optional public-config gate. */
  flag?: NavFlag;
  /** sort key within the group (ascending). */
  order?: number;
}

/** A resolved group: its meta plus the tabs registered into it. */
export interface AdminGroup extends AdminGroupMeta {
  tabs: AdminTabDef[];
}
