// Shared settings-registration seam (types).
//
// Same dependency-inversion discipline as the nav seam: the shell holds the
// registry; each domain registers its own settings section (which imports that
// domain's tab components). The shell imports no domain code.
import type { ComponentType, ReactNode } from "react";

import type { NavFlag } from "@shared/shared/nav/types";
import type { SectionWidth } from "@shared/layout/SectionPage";

/** One tab within a settings section. `render` is a thunk so a domain's tab
 *  body (and its imports) is only pulled when that domain registers — the host
 *  composition decides which domains register, so an isolated build drops the
 *  rest. */
export interface SettingsTab {
  /** stable id, also the ?tab= value. */
  id: string;
  label: string;
  render: () => ReactNode;
  /** optional leading glyph in the section rail. */
  icon?: ComponentType<{ className?: string }>;
  /** supporting copy under the title; the section shell renders it. */
  description?: string;
  /** content width for the body (default: "default"). The SAME named scale
   *  the admin seam uses (both point at the layout's {@link SectionWidth}), so
   *  the two subsystems share one vocabulary rather than drifting apart. */
  width?: SectionWidth;
  /** optional public-config gate; the tab is hidden when the flag is off. */
  flag?: NavFlag;
  /** sort key within the section (ascending). */
  order?: number;
}

/** A domain's group of settings tabs, rendered under a group header in the
 *  Settings tab strip — the visible per-domain grouping (Account / CatalystBooks
 *  / Boomtime). */
export interface SettingsSection {
  /** stable id — a domain key or "account". */
  id: string;
  /** group header shown in the section rail. */
  label: string;
  /** sort key across sections (ascending). */
  order?: number;
  /** optional glyph for the group header. */
  icon?: ComponentType<{ className?: string }>;
  /** one-line description of what this domain's settings cover. */
  description?: string;
  tabs: SettingsTab[];
}
