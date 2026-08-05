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
import { NavLink, Outlet } from "react-router";
import { Page } from "@/layout/Page";
import { PageTabStrip, pageTabClass } from "@/layout/PageTabs";

const TABS = [
  { id: "users", label: "Users", to: "/app/admin/users" },
  { id: "labels", label: "Labels", to: "/app/admin/labels" },
  { id: "backfill", label: "Backfill", to: "/app/admin/backfill" },
  { id: "logs", label: "Logs", to: "/app/admin/logs" },
] as const;

export function AdminPage() {
  return (
    <Page>
      <Page.Header title="Admin" />
      <Page.Body>
        <Page.Content>
          <PageTabStrip ariaLabel="Admin sections">
            {TABS.map((t) => (
              <NavLink
                key={t.id}
                to={t.to}
                role="tab"
                end
                className={({ isActive }) =>
                  pageTabClass(
                    isActive,
                    "font-mono text-xs font-semibold uppercase tracking-widest",
                  )
                }
              >
                {t.label}
              </NavLink>
            ))}
          </PageTabStrip>

          {/* Sub-route mount. Each child owns its own max-width — labels wants
              the full 6xl for the wide catalog table, backfill fits in 4xl,
              logs runs full-bleed. */}
          <div>
            <Outlet />
          </div>
        </Page.Content>
      </Page.Body>
    </Page>
  );
}
