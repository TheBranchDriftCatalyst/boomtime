// LabsTab — Settings > Labs (gaka-lzr reachability). A discoverable home for
// every experimental FEATURE_FLAGS toggle (overviewEditor, labels3D, ...) so
// they aren't only reachable from the public-profile dossier's flags-flipper
// menu. Flags stay default-off; this tab just makes them reachable + labeled.
import {
  Card,
  CardContent,
  CardHeader,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { Switch } from "@thebranchdriftcatalyst/catalyst-ui/ui/switch";
import { LabsFlags } from "@/features/settings/LabsFlags";
import { useBetaRegistration, activateBeta } from "@/features/onboarding/betaRegistration";
import { usePublicConfig } from "@/lib/usePublicConfig";

// The beta-registration preview (gaka-93f.1.2) isn't a FEATURE_FLAGS entry —
// it's a session-scoped, server-vetoable preview toggle (see
// betaRegistration.ts) with different persistence semantics (sessionStorage,
// not localStorage; can be killed instance-wide via /api/v1/config/public).
// It's still a "flip an experimental thing on" control a user would expect
// to find here, so it gets one extra row below the generic flag list rather
// than being force-fit into FEATURE_FLAGS.
function BetaRegistrationRow() {
  const { active, exit } = useBetaRegistration();
  const { config } = usePublicConfig();
  const serverAllows = config.beta_flags.user_registration !== false;

  return (
    <label
      className="flex items-center justify-between gap-3 rounded-lg border border-border p-3"
      data-testid="labs-beta-registration-row"
    >
      <span className="flex flex-col gap-0.5">
        <span className="text-sm font-medium">Beta registration / onboarding flow</span>
        <span className="text-xs text-muted-foreground">
          Preview the new onboarding UX while still logged in (same effect as{" "}
          <code className="rounded bg-muted px-1 py-0.5 text-[11px]">
            ?enable_beta_user_registration=true
          </code>
          ).
          {!serverAllows && " Disabled instance-wide by an admin."}
        </span>
      </span>
      <Switch
        checked={active}
        onCheckedChange={(v) => (v ? activateBeta() : exit())}
        disabled={!serverAllows}
        aria-label="Beta registration / onboarding flow"
        data-testid="labs-beta-registration-switch"
      />
    </label>
  );
}

export function LabsTab() {
  return (
    <Card data-testid="labs-tab">
      <CardHeader className="p-4 pb-0">
        <h2 className="text-lg font-semibold">Labs</h2>
        <p className="text-sm text-muted-foreground">
          Experimental features. Off by default — flip one on to try it, flip it
          back off any time. These are per-browser preferences, not shared with
          anyone else who views your data.
        </p>
      </CardHeader>
      <CardContent className="flex max-w-lg flex-col gap-3 p-4">
        <LabsFlags variant="panel" />
        <BetaRegistrationRow />
      </CardContent>
    </Card>
  );
}
