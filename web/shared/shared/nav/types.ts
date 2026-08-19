// Shared nav-registration seam (types).
//
// PORTABILITY + DEPENDENCY-INVERSION: `web/src/shared/` is the self-contained
// shared FE boundary that will physically relocate to a root `web/shared/` in
// the full-stack domain colocation phase. It imports ZERO domain/feature code —
// domains push their nav entries INTO this registry via registerNavItem(). The
// shell never pulls a domain, so a standalone books build (which only registers
// the books domain) tree-shakes every boomtime/code-domain module away.
import type { ComponentType } from "react";

/** Public-config feature flags a nav entry may gate on (kept in lock-step with
 *  the boolean keys on PublicConfig that actually toggle nav surface). */
export type NavFlag = "books_enabled";

/** A single top-level nav destination. */
export interface NavItem {
  name: string;
  icon: ComponentType<{ className?: string }>;
  to: string;
  /** exact-match active state (react-router `end`). */
  end?: boolean;
  /** when set, the entry only renders if this public-config flag is on. */
  flag?: NavFlag;
  /** optional stable test id, forwarded to the rendered NavLink. */
  testId?: string;
  /** sort key within a section (ascending; unset sorts last, stable). */
  order?: number;
}

/** Identity + presentation for a section a domain contributes items to. A
 *  section WITHOUT a label renders its items flat (top-level, ungrouped); a
 *  section WITH a label renders an uppercase header above its items — the
 *  visible "this belongs to domain X" grouping. */
export interface NavSectionMeta {
  /** stable id — a domain key ("boomtime", "books") or "core"/"config". */
  id: string;
  /** uppercase section header; omit for an ungrouped top-level cluster. */
  label?: string;
  /** sort key across sections (ascending). */
  order?: number;
}

/** A fully-resolved section: its meta plus the items registered into it. */
export interface NavSection extends NavSectionMeta {
  items: NavItem[];
}
