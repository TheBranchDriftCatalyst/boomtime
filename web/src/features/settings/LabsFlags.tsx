// LabsFlags — the single source of truth for rendering the FEATURE_FLAGS
// registry (web/src/lib/featureFlags.ts) as labeled toggles + descriptions
// (gaka-lzr: Settings "Labs" reachability).
//
// Extracted from publicprofile/ProfileChrome.tsx's FlagsFlipper so the
// experimental in-app editor (overviewEditor) and the 3D medallions
// (labels3D) — plus any future flag — have ONE discoverable home in the
// main app (Settings > Labs), not just the public-profile dossier menu.
// FlagsFlipper now reuses this component too, so there is exactly one place
// that knows how to render a flag row.
//
// Flags stay DEFAULT-OFF (push-safety, per featureFlags.ts's contract) —
// this component only makes them toggleable, it never changes a default.
import { FEATURE_FLAGS, useFeatureFlag, type FeatureFlagDef } from "@/lib/featureFlags";
import { Switch } from "@thebranchdriftcatalyst/catalyst-ui/ui/switch";

export interface FlagToggleRowProps {
  flag: FeatureFlagDef;
  /** "panel" (Settings > Labs — bordered card row) or "menu" (compact, for
   * use inside a DropdownMenuContent). Defaults to "panel". */
  variant?: "panel" | "menu";
}

/** One flag's label + description + Switch. The only place that reads/writes
 * a flag's value (via useFeatureFlag) — every consumer renders this. */
export function FlagToggleRow({ flag, variant = "panel" }: FlagToggleRowProps) {
  const [on, set] = useFeatureFlag(flag.key);
  return (
    <label
      className={
        variant === "panel"
          ? "flex items-center justify-between gap-3 rounded-lg border border-border p-3"
          : "flex items-center justify-between gap-3 px-2 py-1.5 text-sm"
      }
      data-testid={`flag-${flag.key}`}
    >
      <span className="flex flex-col gap-0.5">
        <span className={variant === "panel" ? "text-sm font-medium" : "font-medium"}>
          {flag.label}
        </span>
        {flag.description ? (
          <span className="text-xs text-muted-foreground">{flag.description}</span>
        ) : null}
      </span>
      <Switch
        checked={on}
        onCheckedChange={(v) => set(Boolean(v))}
        aria-label={flag.label}
        data-testid={`flag-switch-${flag.key}`}
      />
    </label>
  );
}

export interface LabsFlagsProps {
  variant?: "panel" | "menu";
}

/** Renders every FEATURE_FLAGS entry as a FlagToggleRow. Both the Settings
 * "Labs" tab and the public-profile dossier's FlagsFlipper render THIS —
 * add a flag once (in featureFlags.ts) and it shows up in both places. */
export function LabsFlags({ variant = "panel" }: LabsFlagsProps) {
  return (
    <div
      className={variant === "panel" ? "flex flex-col gap-3" : "flex flex-col"}
      data-testid="labs-flags-list"
    >
      {FEATURE_FLAGS.map((f) => (
        <FlagToggleRow key={f.key} flag={f} variant={variant} />
      ))}
    </div>
  );
}
