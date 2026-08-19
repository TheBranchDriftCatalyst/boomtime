// ProfileChrome — floating dossier controls for /p/:slug (gaka-174.2/.5).
//
// One fixed cluster holding the feature-flag "flipper" + the theme switcher,
// rendered in BOTH read (PublicDashboard) and edit (ProfileEditor) modes.
// Flipping the theme also fires the reclassify sweep (ReclassifyOverlay reacts
// to the same `theme` value).
//
// Theme note: catalyst-ui's setTheme persists app-globally (localStorage); a
// visitor re-skinning changes THEIR preference only. Feature flags are the
// same — per-browser viewer preferences, default-off.
import { useEffect, useRef, useState } from "react";
import { CalendarRange, FlaskConical, Palette } from "lucide-react";
import {
  useTheme,
  THEME_REGISTRY,
} from "@thebranchdriftcatalyst/catalyst-ui/contexts/Theme";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dropdown-menu";
import { FEATURE_FLAGS, useFeatureFlag } from "@shared/lib/featureFlags";
import { RANGE_PRESETS, useProfileRange } from "./profileRange";

function RangeControl() {
  const [days, setDays] = useProfileRange();
  const current = RANGE_PRESETS.find((r) => r.days === days);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="dossier-control__btn"
          title="Service-record window"
          aria-label="Change stats date range"
          data-testid="profile-range-control"
        >
          <CalendarRange size={12} aria-hidden />
          <span className="dossier-control__btn-label">
            {current?.label ?? `${days}D`}
          </span>
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-44">
        <DropdownMenuLabel>Service record</DropdownMenuLabel>
        <DropdownMenuRadioGroup
          value={String(days)}
          onValueChange={(v) => setDays(Number(v))}
        >
          {RANGE_PRESETS.map((r) => (
            <DropdownMenuRadioItem key={r.days} value={String(r.days)}>
              Last {r.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function ThemeControl() {
  const { theme, setTheme } = useTheme();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="dossier-control__btn"
          title="Reskin dossier"
          aria-label="Change dossier theme"
          data-testid="profile-theme-control"
        >
          <Palette size={12} aria-hidden />
          <span className="dossier-control__btn-label">{theme}</span>
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
        <DropdownMenuLabel>Dossier skin</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={theme} onValueChange={setTheme}>
          {THEME_REGISTRY.map((t) => (
            <DropdownMenuRadioItem key={t.name} value={t.name}>
              {t.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function FlagItem({
  flagKey,
  label,
  description,
}: {
  flagKey: string;
  label: string;
  description?: string;
}) {
  const [on, set] = useFeatureFlag(flagKey);
  return (
    <DropdownMenuCheckboxItem
      checked={on}
      onCheckedChange={(v) => set(Boolean(v))}
      data-testid={`flag-${flagKey}`}
    >
      <span className="flex flex-col">
        <span>{label}</span>
        {description ? (
          <span className="text-[10px] text-muted-foreground">{description}</span>
        ) : null}
      </span>
    </DropdownMenuCheckboxItem>
  );
}

// The "flipper" — toggles experimental viewer-preference feature flags.
function FlagsFlipper() {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          className="dossier-control__btn"
          title="Feature flippers"
          aria-label="Feature flags"
          data-testid="profile-flags-flipper"
        >
          <FlaskConical size={12} aria-hidden />
          <span className="dossier-control__btn-label">flags</span>
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-72">
        <DropdownMenuLabel>Experimental</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {FEATURE_FLAGS.map((f) => (
          <FlagItem
            key={f.key}
            flagKey={f.key}
            label={f.label}
            description={f.description}
          />
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

export function DossierControls({
  placement = "br",
}: {
  // "br" = bottom-right (read/preview), "bl" = bottom-left (edit mode, where
  // the Save/Discard chrome owns bottom-right).
  placement?: "br" | "bl";
}) {
  return (
    <div
      className={
        "dossier-control" + (placement === "bl" ? " dossier-control--bl" : "")
      }
      data-testid="profile-controls"
    >
      <FlagsFlipper />
      <RangeControl />
      <ThemeControl />
    </div>
  );
}

// One-shot "RECLASSIFYING" sweep on every theme change. Purely decorative;
// gated behind prefers-reduced-motion in CSS, pointer-events:none.
export function ReclassifyOverlay() {
  const { theme } = useTheme();
  const [playKey, setPlayKey] = useState(0);
  const firstRender = useRef(true);

  useEffect(() => {
    if (firstRender.current) {
      firstRender.current = false;
      return;
    }
    setPlayKey((k) => k + 1);
  }, [theme]);

  if (playKey === 0) return null;
  return <div key={playKey} className="dossier-reclassify" aria-hidden />;
}
