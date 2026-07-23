import { Moon, Palette, Sun } from "lucide-react";
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

/**
 * Full theme + variant + effects switcher. Replaces the minimal sun/moon
 * ThemeToggle in favor of a dev-oriented dropdown surfacing every knob the
 * catalyst-ui ThemeProvider exposes: pick any registered theme (Boomtime,
 * Dracula, Nord, …), flip the variant, and toggle each SYNTHWAVE effect
 * layer independently.
 */
export function ThemeSwitcher() {
  const { theme, setTheme, variant, setVariant, effects, updateEffect } =
    useTheme();
  const isDark = variant === "dark";
  const VariantIcon = isDark ? Sun : Moon;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          title="Theme & effects"
          aria-label="Open theme switcher"
          className="inline-flex h-9 items-center gap-1.5 rounded-md border border-input bg-background px-2.5 text-xs font-medium text-foreground shadow-sm transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background"
        >
          <Palette className="h-4 w-4" aria-hidden="true" />
          <span className="hidden sm:inline">{theme}</span>
          <VariantIcon className="h-3.5 w-3.5 opacity-70" aria-hidden="true" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-56">
        <DropdownMenuLabel>Theme</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={theme} onValueChange={setTheme}>
          {THEME_REGISTRY.map((t) => (
            <DropdownMenuRadioItem key={t.name} value={t.name}>
              {t.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>

        <DropdownMenuSeparator />
        <DropdownMenuLabel>Variant</DropdownMenuLabel>
        <DropdownMenuRadioGroup
          value={variant}
          onValueChange={(v) => setVariant(v as "light" | "dark")}
        >
          <DropdownMenuRadioItem value="light">Light</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="dark">Dark</DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>

        <DropdownMenuSeparator />
        <DropdownMenuLabel>Effects</DropdownMenuLabel>
        <DropdownMenuCheckboxItem
          checked={effects.glow}
          onCheckedChange={(v) => updateEffect("glow", Boolean(v))}
        >
          Glow
        </DropdownMenuCheckboxItem>
        <DropdownMenuCheckboxItem
          checked={effects.scanlines}
          onCheckedChange={(v) => updateEffect("scanlines", Boolean(v))}
        >
          Scanlines
        </DropdownMenuCheckboxItem>
        <DropdownMenuCheckboxItem
          checked={effects.borderAnimations}
          onCheckedChange={(v) => updateEffect("borderAnimations", Boolean(v))}
        >
          Border animations
        </DropdownMenuCheckboxItem>
        <DropdownMenuCheckboxItem
          checked={effects.gradientShift}
          onCheckedChange={(v) => updateEffect("gradientShift", Boolean(v))}
        >
          Gradient shift
        </DropdownMenuCheckboxItem>
        <DropdownMenuCheckboxItem
          checked={effects.debug}
          onCheckedChange={(v) => updateEffect("debug", Boolean(v))}
        >
          Debug
        </DropdownMenuCheckboxItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
