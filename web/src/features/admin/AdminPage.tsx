// AdminPage — top-level /app/admin section (gaka-ebq).
//
// The Admin tab bar mirrors Settings' visual language: horizontal strip
// under a page toolbar, JetBrains Mono uppercase labels, crimson underline
// on the active tab. Uses NavLink+child routes (not ?tab=…) so each
// sub-page is a real URL — deep-linkable, browser-history-friendly, and
// the active-tab styling comes free from NavLink's `isActive`.
//
// Children are lazy-loaded at the route boundary (see App.tsx). This
// component itself only owns the shell + tab strip.
import { useMemo } from "react";
import { NavLink, Outlet } from "react-router";
import { Page } from "@/layout/Page";
import { TabNav, tabClass } from "@/layout/PageTabs";
import { useHeaderSlot } from "@/layout/HeaderSlot";

const TABS = [
  { id: "users", label: "Users", to: "/app/admin/users" },
  { id: "labels", label: "Labels", to: "/app/admin/labels" },
  { id: "cli", label: "Commands", to: "/app/admin/cli" },
  { id: "jobs", label: "Jobs", to: "/app/admin/jobs" },
  { id: "books", label: "Books", to: "/app/admin/books" },
  { id: "data", label: "Data", to: "/app/admin/data" },
  { id: "logs", label: "Logs", to: "/app/admin/logs" },
] as const;

export function AdminPage() {
  // gaka-5jp: the tab strip is HOISTED into the app HeaderBar via useHeaderSlot,
  // reclaiming the Page.Header title row. NavLink computes its own active state
  // from the URL, so this node's identity never needs to change — memoize with
  // an empty dep set so the header slot stays stable. The "Admin" prefix keeps
  // context now that the title row is gone.
  const headerTabs = useMemo(
    () => (
      <TabNav ariaLabel="Admin sections" variant="header" label="Admin">
        {TABS.map((t) => (
          <NavLink
            key={t.id}
            to={t.to}
            role="tab"
            end
            className={({ isActive }) => tabClass(isActive)}
          >
            {t.label}
          </NavLink>
        ))}
      </TabNav>
    ),
    [],
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
