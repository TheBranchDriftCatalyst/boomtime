import { Suspense, useMemo } from "react";
import { Navigate, useSearchParams } from "react-router";
import { Spinner } from "@thebranchdriftcatalyst/catalyst-ui/ui/spinner";
import { SectionPage } from "@shared/layout/SectionPage";
import type { SectionRailGroup } from "@shared/layout/SectionRail";
import {
  getSettingsSections,
  getSettingsTabs,
} from "@shared/shared/settings/registry";

// Settings is a THIN consumer of the settings-registration seam. The tab
// bodies + their grouping (Account / CatalystBooks / Boomtime) are registered by
// each domain's module (web/src/domains/*/register.tsx); this page only resolves
// the active tab from ?tab= and hands the rest to <SectionPage>.
//
// gaka-4x33: the grouped tab strip this used to hoist into the app HeaderBar is
// now a vertical rail inside the content column, exactly as Admin does it — the
// two sections are the same shape and now share one implementation. Settings
// switches on a query param rather than a route, which is why its rail entries
// carry `onSelect`/`active` instead of `to`; that fork is the only difference
// between the two call sites.
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

  // Rail groups mirror the registry's sections. Memoized on the active tab so
  // the identity only changes when the highlight actually moves.
  const railGroups: SectionRailGroup[] = useMemo(
    () =>
      sections.map((section) => ({
        id: section.id,
        label: section.label,
        items: section.tabs.map((t) => ({
          id: t.id,
          label: t.label,
          icon: t.icon,
          active: t.id === active,
          onSelect: () => setParams({ tab: t.id }, { replace: true }),
        })),
      })),
    [sections, active, setParams],
  );

  if (redirect) {
    // Fire via <Navigate> so it routes through the router (scroll/state).
    // Every hook above runs unconditionally, so this early return is legal.
    return <Navigate to={redirect} replace />;
  }

  const tab = tabs.find((t) => t.id === active)!;

  return (
    <SectionPage
      ariaLabel="Settings sections"
      groups={railGroups}
      title={tab.label}
      description={tab.description}
      // Width comes from the registry now. Profile was the one tab that needed
      // the extra room, and it used to be special-cased right here with an
      // inline ternary on the tab id — the kind of shell-side exception every
      // new wide tab had to be added to by hand.
      width={tab.width ?? "default"}
    >
      <div role="tabpanel">
        {/* Tab bodies are registered as lazy() components (each keeps its own
            chunk), so a switch suspends until the chunk loads. */}
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
    </SectionPage>
  );
}
