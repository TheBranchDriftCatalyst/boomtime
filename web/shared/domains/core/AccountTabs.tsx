// Core domain — account-level settings tab bodies. Split out from register.tsx
// so the register module can lazy-import them: registration stays cheap (no
// eager pull of these cards into the app-entry chunk), and each tab body keeps
// its own code-split chunk like it had when it lived in the lazy Settings page.
import { AvatarTab } from "@shared/features/settings/avatar/AvatarTab";
import { ChangePasswordCard } from "@shared/features/settings/ChangePasswordCard";
import { PublicProfileCard } from "@shared/features/settings/PublicProfileCard";
import { TimezoneCard } from "@shared/features/settings/TimezoneCard";
import { PluginSetup } from "@shared/features/settings/PluginSetup";
import { TokensTab } from "@shared/features/tokens/TokensTab";

// ProfileTab: account-level cards (change password + avatar + timezone + public
// profile toggle).
export function ProfileTab() {
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
// use" (API tokens) belong together.
export function PluginAndTokensTab() {
  return (
    <div className="space-y-6">
      <PluginSetup />
      <TokensTab />
    </div>
  );
}
