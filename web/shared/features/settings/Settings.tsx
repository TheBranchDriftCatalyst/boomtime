import { Suspense, useMemo } from "react";
import { Navigate, useSearchParams } from "react-router";
import { Spinner } from "@thebranchdriftcatalyst/catalyst-ui/ui/spinner";
import { Page } from "@shared/layout/Page";
import { GroupedTabNav, tabClass } from "@shared/layout/PageTabs";
import { useHeaderSlot } from "@shared/layout/HeaderSlot";
import {
  getSettingsSections,
  getSettingsTabs,
} from "@shared/shared/settings/registry";

// Settings is now a THIN consumer of the settings-registration seam. The tab
// bodies + their grouping (Account / CatalystBooks / Boomtime) are registered by
// each domain's module (web/src/domains/*/register.tsx); this page only resolves
// the active tab from ?tab= and renders the grouped strip + the active body.
//
// gaka-ebq: the legacy ?tab=logs / ?tab=admin values redirect to
// /app/admin/{logs,labels}; gaka-gud: ?tab=goals redirects to /app/goals. The
// remap lives in the active-tab resolver so bookmarks + old links keep working.
const LEGACY_ADMIN_TAB_REDIRECTS: Record<string, string> = {
  logs: "/app/admin/logs",
  admin: "/app/admin/labels",
  goals: "/app/goals",
};

// Tabs that were merged/moved keep their old ?tab= value working (gaka-books):
// avatar now lives in Profile; API tokens merged into the Plugin tab.
const SETTINGS_TAB_ALIASES: Record<string, string> = {
  avatar: "profile",
  tokens: "plugin",
};

export function Settings() {
  const [params, setParams] = useSearchParams();

  // The registry is static after app-entry registration, so snapshot it once —
  // a fresh array each render would thrash the memoized header slot.
  const sections = useMemo(() => getSettingsSections(), []);
  const tabs = useMemo(() => getSettingsTabs(), []);

  const raw = params.get("tab") ?? "";
  const redirect = LEGACY_ADMIN_TAB_REDIRECTS[raw];
  const aliased = SETTINGS_TAB_ALIASES[raw] ?? raw;
  // Default lands on Plugin Setup so a first-run user still sees the ingest URL
  // immediately (Profile is opt-in via ?tab=profile / the avatar menu).
  const active = tabs.some((t) => t.id === aliased) ? aliased : "plugin";

  // gaka-5jp: the tab strip is HOISTED into the app HeaderBar (reclaiming the
  // whole Page.Header title row). Built once per active-tab change + memoized so
  // useHeaderSlot's identity-keyed effect doesn't thrash the header. The
  // "Settings" prefix keeps page context now that the title row is gone.
  const headerTabs = useMemo(
    () =>
      redirect ? null : (
        <GroupedTabNav
          ariaLabel="Settings sections"
          variant="header"
          label="Settings"
          groups={sections.map((s) => ({
            id: s.id,
            label: s.label,
            children: s.tabs.map((t) => (
              <button
                key={t.id}
                role="tab"
                aria-selected={t.id === active}
                onClick={() => setParams({ tab: t.id }, { replace: true })}
                className={tabClass(t.id === active)}
              >
                {t.label}
              </button>
            )),
          }))}
        />
      ),
    [active, redirect, setParams, sections],
  );
  useHeaderSlot(headerTabs);

  if (redirect) {
    // Fire via <Navigate> so it routes through the router (scroll/state).
    // Hooks above run unconditionally (headerTabs is null on a redirect render)
    // so this early return is legal.
    return <Navigate to={redirect} replace />;
  }

  const tab = tabs.find((t) => t.id === active)!;

  return (
    <Page>
      <Page.Body>
        <Page.Content>
          <div
            role="tabpanel"
            className={active === "profile" ? "max-w-6xl" : "max-w-4xl"}
          >
            {/* Tab bodies are registered as lazy() components (each keeps its
                own chunk), so a switch suspends until the chunk loads. */}
            <Suspense
              fallback={
                <div className="flex h-[40vh] items-center justify-center">
                  <Spinner />
                </div>
              }
            >
              {tab.render()}
            </Suspense>
          </div>
        </Page.Content>
      </Page.Body>
    </Page>
  );
}
