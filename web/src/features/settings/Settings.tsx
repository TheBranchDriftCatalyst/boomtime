import { useMemo, type ComponentType } from "react";
import { Navigate, useSearchParams } from "react-router";
import { BookOpen, Github, Library, Plug, ShieldCheck } from "lucide-react";
import { Page } from "@/layout/Page";
import { TabNav, tabClass } from "@/layout/PageTabs";
import { useHeaderSlot } from "@/layout/HeaderSlot";
import { CurationTab } from "@/features/curation/CurationTab";
import { RemappingsTab } from "@/features/curation/RemappingsTab";
import { WidgetLinksCard } from "@/features/widgets/WidgetLinksCard";
import { Changelog } from "@/features/changelog/Changelog";
// gaka-ebq: Admin / Logs tabs moved OUT of Settings into
// /app/admin. Keep this file lean and non-admin-only.
import { AvatarTab } from "@/features/settings/avatar/AvatarTab";
import { ChangePasswordCard } from "@/features/settings/ChangePasswordCard";
import { GithubConnectCard } from "@/features/settings/GithubConnectCard";
import { AmazonConnectCard } from "@/features/settings/AmazonConnectCard";
import { HardcoverConnectCard } from "@/features/settings/HardcoverConnectCard";
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
      <AvatarTab />
      <TimezoneCard />
      <PublicProfileCard />
    </div>
  );
}

// PluginAndTokensTab: "how to send data" (plugin setup) + "which credential to
// use" (API tokens) belong together — merged into one tab.
function PluginAndTokensTab() {
  return (
    <div className="space-y-6">
      <PluginSetup />
      <TokensTab />
    </div>
  );
}

// ConnectionsTab: every external-account link in one place. Grew out of Profile
// once there were three (Authentik sign-in, GitHub, Amazon for Kindle/Audible);
// each card self-gates (renders nothing when its feature is off), so the tab
// stays tidy per deployment. The header frames the "data fusion" story —
// boomtime pulls each linked account's signal into one dashboard.
function ProviderChip({
  icon: Icon,
  label,
}: {
  icon: ComponentType<{ className?: string }>;
  label: string;
}) {
  return (
    <span className="inline-flex items-center gap-1.5 rounded-full border border-primary/25 bg-primary/5 px-2.5 py-1 text-xs text-foreground/80">
      <Icon className="h-3.5 w-3.5 text-primary" />
      {label}
    </span>
  );
}

function ConnectionsTab() {
  return (
    <div className="space-y-6">
      <div className="relative overflow-hidden rounded-xl border border-primary/20 bg-gradient-to-br from-primary/10 via-background to-background p-6">
        {/* neon bloom + faint grid — synthwave chrome, purely decorative */}
        <div className="pointer-events-none absolute -right-20 -top-24 h-56 w-56 rounded-full bg-primary/20 blur-3xl" />
        <div
          className="pointer-events-none absolute inset-0 opacity-[0.06]"
          style={{
            backgroundImage:
              "linear-gradient(hsl(var(--primary)) 1px, transparent 1px), linear-gradient(90deg, hsl(var(--primary)) 1px, transparent 1px)",
            backgroundSize: "28px 28px",
          }}
        />
        <div className="relative">
          <div className="flex items-center gap-1.5 font-mono text-[11px] uppercase tracking-[0.2em] text-primary/80">
            <Plug className="h-3.5 w-3.5" />
            Data fusion
          </div>
          <h2 className="mt-2 text-2xl font-semibold tracking-tight">Connections</h2>
          <p className="mt-1.5 max-w-xl text-sm text-muted-foreground">
            Link an external account and boomtime fuses its signal into your dashboard — your
            sign-in identity, your GitHub activity, and your Kindle&nbsp;+&nbsp;Audible reading, all
            in one place.
          </p>
          <div className="mt-4 flex flex-wrap gap-2">
            <ProviderChip icon={ShieldCheck} label="Authentik" />
            <ProviderChip icon={Github} label="GitHub" />
            <ProviderChip icon={BookOpen} label="Kindle + Audible" />
            <ProviderChip icon={Library} label="Hardcover" />
          </div>
        </div>
      </div>

      <div className="space-y-6">
        <LinkedIdentitiesCard />
        <GithubConnectCard />
        <AmazonConnectCard />
        <HardcoverConnectCard />
      </div>
    </div>
  );
}

// Profile leads (account-level operations: change password, public profile,
// later Wakatime key, later notifications). Plugin Setup follows — highest-
// value first-run info. API tokens sits adjacent because Plugin Setup
// explains "how to send data" and Tokens explains "which credential to use".
//
// gaka-ebq: Admin / Logs tabs have moved out of this file into
// the top-level /app/admin section. Settings is now non-admin-only and the
// tab list is stable across users.
const TABS = [
  { id: "profile", label: "Profile", render: () => <ProfileTab /> },
  // gaka-books: all external-account links (Authentik / GitHub / Amazon) live
  // here now that there are three. Cards self-gate on their feature flags.
  { id: "connections", label: "Connections", render: () => <ConnectionsTab /> },
  // gaka-9v4 avatar moved into Profile; plugin + API tokens merged (old ?tab=
  // avatar/tokens alias to profile/plugin below so bookmarks keep working).
  { id: "plugin", label: "Plugin & tokens", render: () => <PluginAndTokensTab /> },
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
// gaka-ebq: the legacy ?tab=logs / ?tab=admin values now redirect to
// /app/admin/{logs,labels}. The remap lives in the active-tab resolver
// below so bookmarks + old links keep working.
const LEGACY_ADMIN_TAB_REDIRECTS: Record<string, string> = {
  logs: "/app/admin/logs",
  admin: "/app/admin/labels",
  // gaka-gud: Goals moved out of Settings to its own top-level page.
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

  const raw = params.get("tab") ?? "";
  const redirect = LEGACY_ADMIN_TAB_REDIRECTS[raw];
  const aliased = SETTINGS_TAB_ALIASES[raw] ?? raw;
  const active: TabID = TABS.some((t) => t.id === aliased)
    ? (aliased as TabID)
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

