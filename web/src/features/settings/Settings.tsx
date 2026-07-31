import { Navigate, useSearchParams } from "react-router";
import { PageToolbar } from "@/components/toolbar/PageToolbar";
import { cn } from "@/lib/utils";
import { CurationTab } from "@/features/curation/CurationTab";
import { RemappingsTab } from "@/features/curation/RemappingsTab";
import { GoalsTab } from "@/features/goals/GoalsTab";
import { WidgetLinksCard } from "@/features/widgets/WidgetLinksCard";
import { Changelog } from "@/features/changelog/Changelog";
// gaka-ebq: Admin / Backfill / Logs tabs moved OUT of Settings into
// /app/admin. Keep this file lean and non-admin-only.
import { AvatarTab } from "@/features/settings/avatar/AvatarTab";
import { ChangePasswordCard } from "@/features/settings/ChangePasswordCard";
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
  // gaka-wpb: user-defined composite goals. Placed after remappings
  // (data-shaping) and before widgets (embed-shaping) so the tab flow
  // reads "what data / what to aim for / how to embed".
  { id: "goals", label: "Goals", render: () => <GoalsTab /> },
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
};

export function Settings() {
  const [params, setParams] = useSearchParams();

  const raw = params.get("tab") ?? "";
  const redirect = LEGACY_ADMIN_TAB_REDIRECTS[raw];
  if (redirect) {
    // Fire the navigation via <Navigate> so it goes through the router
    // and preserves scroll/state behavior. Guard is a top-level early
    // return so tab hunt below never runs on a stale legacy id.
    return <Navigate to={redirect} replace />;
  }

  const active: TabID = TABS.some((t) => t.id === raw)
    ? (raw as TabID)
    : "plugin";
  const tab = TABS.find((t) => t.id === active)!;

  return (
    <div>
      <PageToolbar title="Settings" />

      <div
        role="tablist"
        aria-label="Settings sections"
        className="mb-6 flex gap-1 border-b border-border"
      >
        {TABS.map((t) => (
          <button
            key={t.id}
            role="tab"
            aria-selected={t.id === active}
            onClick={() => setParams({ tab: t.id }, { replace: true })}
            className={cn(
              "-mb-px border-b-2 px-4 py-2 text-sm font-medium transition-colors",
              t.id === active
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground hover:border-border hover:text-foreground",
            )}
          >
            {t.label}
          </button>
        ))}
      </div>

      <div
        role="tabpanel"
        className={active === "profile" ? "max-w-6xl" : "max-w-4xl"}
      >
        {tab.render()}
      </div>
    </div>
  );
}

