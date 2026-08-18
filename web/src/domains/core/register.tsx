// Core (domain-agnostic) registration module.
//
// Registers the FE surface that belongs to NO single domain: the mixed-fusion
// Overview, the Settings entry, the account-level settings tabs, and the
// operational admin tabs (users / data / jobs / cli / logs).
//
// Like every domain module it imports the shell's register API + its own things
// and NOTHING from another domain — the host entry decides it runs (see
// web/src/app/registerDomains.ts). Settings tab BODIES are lazy() so registering
// stays cheap: nothing heavy lands in the app-entry chunk, and each tab keeps
// its own code-split chunk. That laziness also keeps registration side-effect
// free for tests (feature modules aren't evaluated until a tab actually renders).
import { lazy } from "react";
import { LayoutDashboard, Settings2 } from "lucide-react";

import { registerNavItem } from "@/shared/nav/registry";
import { registerSettingsSection } from "@/shared/settings/registry";
import { registerAdminTab } from "@/shared/admin/registry";

const ProfileTab = lazy(() =>
  import("./AccountTabs").then((m) => ({ default: m.ProfileTab })),
);
const PluginAndTokensTab = lazy(() =>
  import("./AccountTabs").then((m) => ({ default: m.PluginAndTokensTab })),
);
const Changelog = lazy(() =>
  import("@/features/changelog/Changelog").then((m) => ({
    default: m.Changelog,
  })),
);

export function registerCoreDomain(): void {
  // ── Nav ────────────────────────────────────────────────────────────────
  // Overview: the mixed-fusion dashboard, first + ungrouped (it fuses BOTH
  // domains, so it deliberately isn't bucketed under one).
  registerNavItem(
    { id: "core", order: 0 },
    { name: "Overview", icon: LayoutDashboard, to: "/app", end: true },
  );
  // Settings: domain-agnostic account/config entry (Logs + Changelog live in it).
  registerNavItem(
    { id: "config", order: 90 },
    { name: "Settings", icon: Settings2, to: "/app/settings" },
  );

  // ── Settings (Account group) ───────────────────────────────────────────
  registerSettingsSection({
    id: "account",
    label: "Account",
    order: 0,
    tabs: [
      { id: "profile", label: "Profile", order: 0, render: () => <ProfileTab /> },
      {
        id: "plugin",
        label: "Plugin & tokens",
        order: 1,
        render: () => <PluginAndTokensTab />,
      },
      {
        id: "changelog",
        label: "Changelog",
        order: 2,
        render: () => <Changelog embedded />,
      },
    ],
  });

  // ── Admin (core / operational group) ───────────────────────────────────
  const core = { id: "core", label: "Operations", order: 0 };
  registerAdminTab({ id: "users", label: "Users", to: "/app/admin/users", group: core, order: 0 });
  registerAdminTab({ id: "data", label: "Data", to: "/app/admin/data", group: core, order: 1 });
  registerAdminTab({ id: "jobs", label: "Jobs", to: "/app/admin/jobs", group: core, order: 2 });
  registerAdminTab({ id: "cli", label: "Commands", to: "/app/admin/cli", group: core, order: 3 });
  registerAdminTab({ id: "logs", label: "Logs", to: "/app/admin/logs", group: core, order: 4 });
}
