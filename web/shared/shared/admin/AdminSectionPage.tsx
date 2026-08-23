// AdminSectionPage — the shared Admin section shell (the formal "AdminPage"
// primitive). It is now a thin adapter: resolve the admin-registration seam
// into rail groups + the active tab's presentation metadata, then hand both to
// the domain-free <SectionPage>. It imports no tab component, so tab bodies
// stay lazy-loaded at the router boundary and the grouping is driven entirely
// by what each domain registered.
//
// boom-4x33: the grouped tab strip this used to hoist into the app HeaderBar
// (via useHeaderSlot) is gone — it now renders as a vertical rail inside the
// content column, and the app header falls back to its own "// Admin" title,
// which it already had a mapping for. Two reasons it had to move: the strip's
// intrinsic width stretched the shell's grid column and clipped the header's
// right-side controls off-viewport (boom-c26s), and nine tabs under three
// group labels is a list, not a tab strip.
import { useMemo } from "react";
import { Outlet, useLocation } from "react-router";
import { SectionPage } from "@shared/layout/SectionPage";
import type { SectionRailGroup } from "@shared/layout/SectionRail";
import { getAdminGroups } from "@shared/shared/admin/registry";
import type { AdminTabDef } from "@shared/shared/admin/types";

export function AdminSectionPage() {
  // Read the registry ONCE per mount. getAdminGroups() derives a fresh array
  // (and fresh group objects) on every call, so reading it raw during render
  // hands every downstream memo a dependency that changes every time — which
  // is what once defeated the header-slot memo and blanked this very route.
  // Registration happens at entry (registerHostDomains) before the first
  // render, so an empty dep list is correct — the same pattern Settings uses.
  const groups = useMemo(() => getAdminGroups(), []);
  const { pathname } = useLocation();

  // Which tab is showing? Matched off the URL rather than threaded down from
  // the route table, so a tab body never has to announce itself and a deep
  // link or back-button lands on the right header with no extra wiring.
  const activeTab: AdminTabDef | undefined = useMemo(() => {
    const tabs = groups.flatMap((g) => g.tabs).filter((t) => !t.external);
    // Longest match wins, so a nested path (/app/admin/labels/42) still
    // resolves to its parent tab instead of falling through to no header.
    return tabs
      .filter((t) => pathname === t.to || pathname.startsWith(`${t.to}/`))
      .sort((a, b) => b.to.length - a.to.length)[0];
  }, [groups, pathname]);

  const railGroups: SectionRailGroup[] = useMemo(
    () =>
      groups.map((g) => ({
        id: g.id,
        label: g.label,
        items: g.tabs.map((t) => ({
          id: t.id,
          label: t.label,
          icon: t.icon,
          to: t.to,
          external: t.external,
        })),
      })),
    [groups],
  );

  return (
    <SectionPage
      ariaLabel="Admin sections"
      groups={railGroups}
      // Falls back to the section name on a path no tab claims, so the header
      // never blinks empty mid-navigation.
      title={activeTab?.label ?? "Admin"}
      description={activeTab?.description}
      width={activeTab?.width ?? "default"}
    >
      <Outlet />
    </SectionPage>
  );
}
