// AdminSectionPage — the shared Admin section shell (the formal "AdminPage"
// primitive). It owns the page chrome only: the POM <Page> body + the
// domain-grouped tab strip hoisted into the app HeaderBar, then an <Outlet/> for
// whichever tab body the router mounted.
//
// It reads the admin-registration seam (getAdminGroups) — it does NOT import any
// tab component. Tab bodies stay lazy-loaded at the router boundary, so this
// shell pulls zero domain code and the grouping is driven entirely by what each
// domain registered.
import { useMemo } from "react";
import { NavLink, Outlet } from "react-router";
import { Page } from "@/layout/Page";
import { GroupedTabNav, tabClass } from "@/layout/PageTabs";
import { useHeaderSlot } from "@/layout/HeaderSlot";
import { getAdminGroups } from "@/shared/admin/registry";

export function AdminSectionPage() {
  const groups = getAdminGroups();

  // Hoist the grouped tab strip into the HeaderBar (reclaims the page title
  // row). NavLink computes active state from the URL so the node identity never
  // needs to change — memoize on the registered groups so the header slot stays
  // stable across unrelated re-renders. The "Admin" prefix keeps page context.
  const headerTabs = useMemo(
    () => (
      <GroupedTabNav
        ariaLabel="Admin sections"
        variant="header"
        label="Admin"
        groups={groups.map((g) => ({
          id: g.id,
          label: g.label,
          children: g.tabs.map((t) => (
            <NavLink
              key={t.id}
              to={t.to}
              role="tab"
              end
              className={({ isActive }) => tabClass(isActive)}
            >
              {t.label}
            </NavLink>
          )),
        }))}
      />
    ),
    [groups],
  );
  useHeaderSlot(headerTabs);

  return (
    <Page>
      <Page.Body>
        <Page.Content>
          {/* Sub-route mount. Each child owns its own max-width — labels wants
              the full 6xl for the wide catalog table, logs runs full-bleed. */}
          <div>
            <Outlet />
          </div>
        </Page.Content>
      </Page.Body>
    </Page>
  );
}
