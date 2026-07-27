import type React from "react";
import { useSearchParams } from "react-router";
import { PageToolbar } from "@/components/toolbar/PageToolbar";
import { cn } from "@/lib/utils";
import { CurationTab } from "@/features/curation/CurationTab";
import { RemappingsTab } from "@/features/curation/RemappingsTab";
import { GoalsTab } from "@/features/goals/GoalsTab";
import { WidgetLinksCard } from "@/features/widgets/WidgetLinksCard";
import { Changelog } from "@/features/changelog/Changelog";
import { Logs } from "@/features/logs/Logs";
import { AdminTab } from "@/features/admin/AdminTab";
import { BackfillTab } from "@/features/admin/BackfillTab";
import { AvatarTab } from "@/features/settings/avatar/AvatarTab";
import { ChangePasswordCard } from "@/features/settings/ChangePasswordCard";
import { DashboardEditorCard } from "@/features/settings/DashboardEditorCard";
import { PluginSetup } from "@/features/settings/PluginSetup";
import { PublicProfileCard } from "@/features/settings/PublicProfileCard";
import { TokensTab } from "@/features/tokens/TokensTab";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";

// ProfileTab: bundles the account-level cards (change password + public
// profile toggle + composable dashboard editor). The editor is gated on
// the profile being enabled — no point letting the owner arrange tiles
// their public URL can't serve.
function ProfileTab() {
  const { data: profile } = useQuery({
    queryKey: qk.publicProfile(),
    queryFn: () => api.getPublicProfile(),
    staleTime: 30_000,
  });
  return (
    <div className="space-y-6">
      <ChangePasswordCard />
      <PublicProfileCard />
      {profile?.enabled && <DashboardEditorCard />}
    </div>
  );
}

// Profile leads (account-level operations: change password, public profile,
// later Wakatime key, later notifications). Plugin Setup follows — highest-
// value first-run info. API tokens sits adjacent because Plugin Setup
// explains "how to send data" and Tokens explains "which credential to use".
//
// gaka-myv: the Admin tab is conditionally appended below when the current
// user is on BOOM_ADMIN_USERS. Keeps the tab list stable for the common
// (non-admin) case and avoids leaking the existence of admin routes to
// arbitrary logged-in users.
const BASE_TABS = [
  { id: "profile", label: "Profile", render: () => <ProfileTab /> },
  // gaka-9v4: AI-generated chibi avatar. Sits next to Profile because
  // the avatar surfaces on the public profile hero — semantically an
  // account-level identity concern, not a "settings you tweak weekly"
  // tab. Kept out of BASE_TABS's default landing so first-run doesn't
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
  { id: "logs", label: "Logs", render: () => <Logs embedded /> },
] as const;

const ADMIN_TAB = {
  id: "admin",
  label: "Admin",
  render: () => <AdminTab />,
} as const;

// gaka-vh8: git-history backfill lives under its own admin tab so the
// existing Admin tab's labels-catalog UX doesn't have to grow a second
// concern. Both share the same "admin only" gate.
const BACKFILL_TAB = {
  id: "backfill",
  label: "Backfill",
  render: () => <BackfillTab />,
} as const;

type BaseTabID = (typeof BASE_TABS)[number]["id"];
type TabID = BaseTabID | typeof ADMIN_TAB.id | typeof BACKFILL_TAB.id;

// Settings: one page, horizontal top tab bar. The active tab lives in
// ?tab=<id> so tabs are linkable/bookmarkable (old /app/logs and
// /app/changelog routes redirect here). Default lands on Plugin Setup so a
// first-run user still sees the ingest URL immediately (Profile is opt-in via
// ?tab=profile / avatar-menu link, not a first-run destination).
export function Settings() {
  const [params, setParams] = useSearchParams();

  // gaka-myv: pull the current-user record just to check is_admin so the
  // Admin tab shows up in the right people's list. Static after login, so a
  // long staleTime keeps this cheap.
  const { data: current } = useQuery({
    queryKey: ["auth", "current-user"],
    queryFn: () => api.currentUser(),
    staleTime: 60_000,
  });
  const isAdmin = Boolean(current?.data?.is_admin);

  const tabs = isAdmin
    ? ([...BASE_TABS, ADMIN_TAB, BACKFILL_TAB] as ReadonlyArray<{ id: string; label: string; render: () => React.ReactNode }>)
    : (BASE_TABS as ReadonlyArray<{ id: string; label: string; render: () => React.ReactNode }>);

  const raw = params.get("tab");
  const active: TabID = tabs.some((t) => t.id === raw)
    ? (raw as TabID)
    : "plugin";
  const tab = tabs.find((t) => t.id === active)!;

  return (
    <div>
      <PageToolbar title="Settings" />

      <div
        role="tablist"
        aria-label="Settings sections"
        className="mb-6 flex gap-1 border-b border-border"
      >
        {tabs.map((t) => (
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
