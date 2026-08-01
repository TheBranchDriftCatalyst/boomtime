// ProfileChrome — floating dossier controls for /p/:slug (gaka-174.2).
//
// Rendered in BOTH read (PublicDashboard) and edit (ProfileEditor) modes.
// Today it surfaces the theme switcher; the date-range control (gaka-174.7)
// joins the same cluster later.
//
// Flipping the theme also fires the "reclassify" sweep — DossierThemeControl
// and ReclassifyOverlay both react to the same `theme` value from useTheme,
// so the transition is a side effect of the switch, not a separate trigger.
//
// Theme note: catalyst-ui's setTheme persists app-globally (localStorage).
// A visitor re-skinning the dossier changes THEIR preference only; baking an
// owner-canonical theme into the profile payload is the follow-up half of
// gaka-174.2 (needs layout-side persistence).
import { useEffect, useRef, useState } from "react";
import { Palette } from "lucide-react";
import {
  useTheme,
  THEME_REGISTRY,
} from "@thebranchdriftcatalyst/catalyst-ui/contexts/Theme";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dropdown-menu";

export function DossierThemeControl({
  placement = "br",
}: {
  // "br" = bottom-right (read/preview), "bl" = bottom-left (edit mode, where
  // the Save/Discard chrome owns bottom-right).
  placement?: "br" | "bl";
}) {
  const { theme, setTheme } = useTheme();
  return (
    <div
      className={
        "dossier-control" + (placement === "bl" ? " dossier-control--bl" : "")
      }
      data-testid="profile-theme-control"
    >
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button
            type="button"
            className="dossier-control__btn"
            title="Reskin dossier"
            aria-label="Change dossier theme"
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
    </div>
  );
}

// One-shot "RECLASSIFYING" sweep on every theme change. Purely decorative;
// the animation itself is gated behind prefers-reduced-motion in CSS, and the
// overlay is pointer-events:none so it never blocks interaction.
export function ReclassifyOverlay() {
  const { theme } = useTheme();
  const [playKey, setPlayKey] = useState(0);
  const firstRender = useRef(true);

  useEffect(() => {
    // Skip the initial mount — only replay on an actual switch.
    if (firstRender.current) {
      firstRender.current = false;
      return;
    }
    setPlayKey((k) => k + 1);
  }, [theme]);

  if (playKey === 0) return null;
  // `key` forces a fresh element each switch so the CSS animation restarts.
  return <div key={playKey} className="dossier-reclassify" aria-hidden />;
}
