// SectionPage — the shared layout for a SECTION: a page that owns a rail of
// sub-destinations plus a body that swaps between them (gaka-4x33).
//
// Admin and Settings are both this shape, and both used to hand-roll it by
// hoisting a grouped tab strip into the app HeaderBar. This composes the same
// job out of POM parts instead:
//
//   ┌──────────────────────────────────────────────┐
//   │ app HeaderBar        // Admin        [search]│  ← shell's, untouched
//   ├───────────┬──────────────────────────────────┤
//   │ OPERATIONS│ Users                  [actions] │  ← title from the registry,
//   │ · Users   │ What each role grants by default.│    actions from the slot
//   │ · Data    ├──────────────────────────────────┤
//   │ BOOMTIME  │                                  │
//   │ · Labels  │   body (the only scroll region)  │
//   └───────────┴──────────────────────────────────┘
//
// Three things it fixes structurally:
//
//   · The rail lives INSIDE the content column, so section nav can never again
//     stretch the shell grid and clip the app header's controls (gaka-c26s).
//   · The title row is rendered ONCE, here, from registry metadata — so tabs
//     stop each inventing their own (three tabs had three different header
//     treatments) and the label in the rail always matches the title above the
//     body, because they're the same string.
//   · Content width comes from a named scale, not a per-tab `max-w-*`, so the
//     column stops visibly jumping as you move between tabs.
//
// DOMAIN-FREE: takes resolved groups + strings. Admin passes route-driven items,
// Settings passes state-driven ones; neither registry is imported here.
import type { ReactNode } from "react";
import { Page } from "@shared/layout/Page";
import { SectionRail, type SectionRailGroup } from "@shared/layout/SectionRail";
import {
  PageActionsProvider,
  usePageActionsNode,
} from "@shared/layout/PageActionsSlot";
import { cn } from "@shared/lib/utils";

/** Named content widths. Callers pass the name; the mapping lives here so a
 *  change to the reading measure is one edit, not a grep across every tab. */
const WIDTH_CLASS = {
  default: "max-w-4xl",
  wide: "max-w-6xl",
  full: "",
} as const;

export type SectionWidth = keyof typeof WIDTH_CLASS;

export interface SectionPageProps {
  /** Accessible name for the rail's nav landmark, e.g. "Admin sections". */
  ariaLabel: string;
  /** Resolved rail groups — see `<SectionRail>`. */
  groups: SectionRailGroup[];
  /** Title above the body. Normally the active entry's label. */
  title: ReactNode;
  /** Supporting copy under the title. */
  description?: ReactNode;
  /** Content width for the body (default: "default"). */
  width?: SectionWidth;
  /** The active entry's body. */
  children: ReactNode;
}

/** The title row. Split into its own component because it READS the actions
 *  slot — and a component that reads a slot must never be the same one that
 *  writes it (see createNodeSlot's read/write-split note). */
function SectionHeader({
  title,
  description,
}: {
  title: ReactNode;
  description?: ReactNode;
}) {
  const actions = usePageActionsNode();
  // Nothing to show at all: a bare-shell tab (no title, no description, no
  // actions) gets its full height back instead of an empty 64px row.
  if (title == null && description == null && actions == null) return null;
  return (
    <Page.Header title={title} subtitle={description}>
      {actions}
    </Page.Header>
  );
}

export function SectionPage({
  ariaLabel,
  groups,
  title,
  description,
  width = "default",
  children,
}: SectionPageProps) {
  return (
    // The provider wraps BOTH the header (reader) and the body (writer) — a
    // slot only bridges components inside one provider. Scoped to the section
    // rather than app-wide so the router unmounting a tab tears its actions
    // down automatically.
    <PageActionsProvider>
      <Page>
        <Page.Body
          nav={
            <Page.Nav>
              <SectionRail ariaLabel={ariaLabel} groups={groups} />
            </Page.Nav>
          }
        >
          {/* The content COLUMN: a pinned title row over the one scroll
              region. Nested rather than using Page.Header at the top level so
              the title sits beside the rail instead of spanning above it —
              the rail is peer navigation, not a child of the tab you're on. */}
          <div className="flex min-h-0 flex-1 flex-col">
            <SectionHeader title={title} description={description} />
            <Page.Content>
              <div className={cn(WIDTH_CLASS[width])}>{children}</div>
            </Page.Content>
          </div>
        </Page.Body>
      </Page>
    </PageActionsProvider>
  );
}
