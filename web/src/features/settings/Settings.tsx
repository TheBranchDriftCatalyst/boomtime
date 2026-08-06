import { useMemo } from "react";
import { Navigate, useSearchParams } from "react-router";
import { Page } from "@/layout/Page";
import { TabNav, tabClass } from "@/layout/PageTabs";
import { useHeaderSlot } from "@/layout/HeaderSlot";
import { CurationTab } from "@/features/curation/CurationTab";
import { RemappingsTab } from "@/features/curation/RemappingsTab";
import { WidgetLinksCard } from "@/features/widgets/WidgetLinksCard";
import { Changelog } from "@/features/changelog/Changelog";
// gaka-ebq: Admin / Backfill / Logs tabs moved OUT of Settings into
// /app/admin. Keep this file lean and non-admin-only.
import { AvatarTab } from "@/features/settings/avatar/AvatarTab";
import { ChangePasswordCard } from "@/features/settings/ChangePasswordCard";
import { LinkedIdentitiesCard } from "@/features/settings/LinkedIdentitiesCard";
import { PluginSetup } from "@/features/settings/PluginSetup";
import { PublicProfileCard } from "@/features/settings/PublicProfileCard";
import { TimezoneCard } from "@/features/settings/TimezoneCard";
import { TokensTab } from "@/features/tokens/TokensTab";

// ProfileTab: bundles the account-level cards (change password + public
// profile toggle). gaka-ie3: the composable dashboard EDITOR was moved
// out of Settings and into the public profile page itself — visiting
// /p/<your-slug> as the owner now renders an inline edit-mode toggle.
// Settings keeps only the enable-toggle + slug field via
// <PublicProfileCard/>, so this tab is the one-stop shop for account-
// level toggles without duplicating the layout editor's chrome.
function ProfileTab() {
  return (
    <div className="space-y-6">
      <ChangePasswordCard />
      <LinkedIdentitiesCard />
      <TimezoneCard />
      <PublicProfileCard />
    </div>
  );
}

// Profile leads (account-level operations: change password, public profile,
// later Wakatime key, later notifications). Plugin Setup follows — highest-
// value first-run info. API tokens sits adjacent because Plugin Setup
// explains "how to send data" and Tokens explains "which credential to use".
//
// gaka-ebq: Admin / Backfill / Logs tabs have moved out of this file into
// the top-level /app/admin section. Settings is now non-admin-only and the
// tab list is stable across users.
const TABS = [
  { id: "profile", label: "Profile", render: () => <ProfileTab /> },
  // gaka-9v4: AI-generated chibi avatar. Sits next to Profile because
  // the avatar surfaces on the public profile hero — semantically an
  // account-level identity concern, not a "settings you tweak weekly"
  // tab. Kept out of TABS's default landing so first-run doesn't
  // fire an LLM stream at random.
  { id: "avatar", label: "Avatar", render: () => <AvatarTab /> },
  { id: "plugin", label: "Plugin setup", render: () => <PluginSetup /> },
  { id: "tokens", label: "API tokens", render: () => <TokensTab /> },
  { id: "curation", label: "Hidden data", render: () => <CurationTab /> },
  { id: "remappings", label: "Remappings", render: () => <RemappingsTab /> },
  // gaka-gud: Goals moved OUT to a top-level /app/goals page (a ?tab=goals
  // redirect below keeps old links working).
  { id: "widgets", label: "Widgets", render: () => <WidgetLinksCard /> },
  { id: "changelog", label: "Changelog", render: () => <Changelog embedded /> },
] as const;

type TabID = (typeof TABS)[number]["id"];

// Settings: one page, horizontal top tab bar. The active tab lives in
// ?tab=<id> so tabs are linkable/bookmarkable (the /app/changelog route
// redirects here). Default lands on Plugin Setup so a first-run user
// still sees the ingest URL immediately (Profile is opt-in via
// ?tab=profile / avatar-menu link, not a first-run destination).
//
// gaka-ebq: the legacy ?tab=logs / ?tab=admin / ?tab=backfill values now
// redirect to /app/admin/{logs,labels,backfill}. The remap lives in the
// active-tab resolver below so bookmarks + old links keep working.
const LEGACY_ADMIN_TAB_REDIRECTS: Record<string, string> = {
  logs: "/app/admin/logs",
  admin: "/app/admin/labels",
  backfill: "/app/admin/backfill",
  // gaka-gud: Goals moved out of Settings to its own top-level page.
  goals: "/app/goals",
};

export function Settings() {
  const [params, setParams] = useSearchParams();

  const raw = params.get("tab") ?? "";
  const redirect = LEGACY_ADMIN_TAB_REDIRECTS[raw];
  const active: TabID = TABS.some((t) => t.id === raw)
    ? (raw as TabID)
    : "plugin";

  // gaka-5jp: the tab strip is HOISTED into the app HeaderBar (reclaiming the
  // whole Page.Header title row). Build it ONCE per active-tab change and
  // memoize — useHeaderSlot's effect keys on this node's identity, so a stable
  // reference keeps it from thrashing the header on unrelated re-renders. The
  // "Settings" prefix keeps page context now that the title row is gone.
  const headerTabs = useMemo(
    () =>
      redirect ? null : (
        <TabNav ariaLabel="Settings sections" variant="header" label="Settings">
          {TABS.map((t) => (
            <button
              key={t.id}
              role="tab"
              aria-selected={t.id === active}
              onClick={() => setParams({ tab: t.id }, { replace: true })}
              className={tabClass(t.id === active)}
            >
              {t.label}
            </button>
          ))}
        </TabNav>
      ),
    [active, redirect, setParams],
  );
  useHeaderSlot(headerTabs);

  if (redirect) {
    // Fire the navigation via <Navigate> so it goes through the router and
    // preserves scroll/state behavior. Hooks above run unconditionally
    // (headerTabs is null on a redirect render) so this early return is legal.
    return <Navigate to={redirect} replace />;
  }

  const tab = TABS.find((t) => t.id === active)!;

  return (
    <Page>
      <Page.Body>
        <Page.Content>
          <div
            role="tabpanel"
            className={active === "profile" ? "max-w-6xl" : "max-w-4xl"}
          >
            {tab.render()}
          </div>
        </Page.Content>
      </Page.Body>
    </Page>
  );
}

