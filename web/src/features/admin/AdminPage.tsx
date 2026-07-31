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
import { PageToolbar } from "@thebranchdriftcatalyst/catalyst-ui/components/PageToolbar";
import { cn } from "@/lib/utils";

const TABS = [
  { id: "labels", label: "Labels", to: "/app/admin/labels" },
  { id: "backfill", label: "Backfill", to: "/app/admin/backfill" },
  { id: "logs", label: "Logs", to: "/app/admin/logs" },
] as const;

export function AdminPage() {
  return (
    <div>
      <PageToolbar title="Admin" />

      <div
        role="tablist"
        aria-label="Admin sections"
        className="mb-6 flex gap-1 border-b border-border"
      >
        {TABS.map((t) => (
          <NavLink
            key={t.id}
            to={t.to}
            role="tab"
            end
            className={({ isActive }) =>
              cn(
                "-mb-px border-b-2 px-4 py-2 font-mono text-xs font-semibold uppercase tracking-widest transition-colors",
                isActive
                  ? "border-primary text-foreground"
                  : "border-transparent text-muted-foreground hover:border-border hover:text-foreground",
              )
            }
          >
            {t.label}
          </NavLink>
        ))}
      </div>

      {/* Sub-route mount. Each child owns its own max-width — labels wants
          the full 6xl for the wide catalog table, backfill fits in 4xl,
          logs runs full-bleed. */}
      <div>
        <Outlet />
      </div>
    </div>
  );
}
