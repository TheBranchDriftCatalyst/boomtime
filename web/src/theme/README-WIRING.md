# Dark mode theme system — wiring instructions

All files below are NEW and self-contained. Apply these edits to the existing
tree once the concurrent import-UI work has merged. Nothing here was applied
automatically.

New files created:

- `web/src/theme/ThemeProvider.tsx` — context + `useTheme()` hook
- `web/src/theme/theme.css` — light/dark design tokens (standalone)
- `web/src/theme/apexTheme.ts` — `useApexTheme()` + `mergeApexTheme()`
- `web/src/components/ThemeToggle.tsx` — sun/moon toggle button
- `web/src/theme/README-WIRING.md` — this file

**Dark is the default** (first visit with nothing stored renders dark).
Preference persists in `localStorage["gakatime-theme"]` as `dark` | `light`
(`system` is also accepted on read). The theme is applied by toggling the
`.dark` class on `<html>` plus `document.documentElement.style.colorScheme`.

---

## (a) CSS: import `theme.css` and DEDUPE against `index.css`

`web/src/index.css` **already defines an equivalent token set**: `:root {...}`,
`.dark {...}`, `@custom-variant dark ...`, and the `@theme inline {...}` block.
So you have two clean options — pick ONE:

**Option 1 (recommended): keep `index.css` as-is, do NOT import `theme.css`.**
The provider/toggle/apex helpers work against whatever tokens exist. The only
improvement worth porting from `theme.css` is the dark-variant selector:

```css
/* in index.css, replace: */
@custom-variant dark (&:is(.dark *));
/* with (so utilities on the `.dark` element itself also apply): */
@custom-variant dark (&:where(.dark, .dark *));
```

**Option 2: make `theme.css` the single source of truth.** Delete the
duplicated blocks from `index.css` (the `:root`, `.dark`, `@custom-variant dark`,
and `@theme inline` blocks), keep `@import "tailwindcss";` and the
`@layer base` / `.apexcharts-*` rules in `index.css`, then add **below** the
Tailwind import in `index.css`:

```css
@import "./theme/theme.css";
```

Do not import it in `main.tsx` — it must live in the same Tailwind entry so the
`@custom-variant` and `@theme inline` directives are processed by
`@tailwindcss/vite`. Do not import BOTH the duplicated blocks and `theme.css`
(that double-defines tokens — harmless but confusing).

> `theme.css` deliberately omits `@import "tailwindcss";`. If you ever use it as
> the *only* stylesheet, add that import at the top.

---

## (b) Wrap the app with `<ThemeProvider>` (outermost) in `main.tsx`

In `web/src/main.tsx`, add the import and wrap the tree so `ThemeProvider` is
the **outermost** provider (before Router / QueryClient):

```tsx
import { ThemeProvider } from "@/theme/ThemeProvider";
// ...
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <AuthProvider>
            <App />
            <Toaster position="top-right" richColors />
          </AuthProvider>
        </BrowserRouter>
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
);
```

The provider reads localStorage synchronously on first render and applies the
`.dark` class via an effect, so no extra setup is needed.

---

## (c) Drop `<ThemeToggle />` into the AppShell topbar

In `web/src/components/AppShell.tsx`, add the import:

```tsx
import { ThemeToggle } from "@/components/ThemeToggle";
```

Then place it in the header — put it just before the "New API token" button so
it sits at the left of the topbar action cluster:

```tsx
<header className="flex h-16 items-center justify-end gap-3 border-b bg-card px-6">
  <ThemeToggle />
  <Button variant="outline" size="sm" onClick={createToken} title="Create a new API token">
    <KeyRound className="h-4 w-4" />
    New API token
  </Button>
  {/* ...existing DropdownMenu... */}
</header>
```

The button already matches the `outline` + `icon` shadcn styling (h-9 w-9,
border-input, hover:bg-accent), so it aligns with the existing controls.

---

## (d) Make charts merge `useApexTheme()` into their ApexCharts options

For each chart under `web/src/components/charts/` (e.g. `ColumnChart.tsx`,
`PieChart.tsx`, `RadarChart.tsx`, `HeatmapChart.tsx`, `HourBarChart.tsx`,
`FileBarChart.tsx`, `TimelineChart.tsx`, `ColumnChart.tsx`):

1. Import the hook + merge helper:

```tsx
import { useApexTheme, mergeApexTheme } from "@/theme/apexTheme";
```

2. Inside the component, read the fragment and merge it LAST into the options
   you already build:

```tsx
const apexTheme = useApexTheme();
const options: ApexOptions = mergeApexTheme(
  {
    chart: { ...baseChart, type: "bar" },
    // ...the chart's existing options...
    grid: { borderColor: "var(--border)", strokeDashArray: 4 },
  },
  apexTheme,
);
```

`mergeApexTheme` shallow-merges `chart`, `grid`, `tooltip`, and `theme` one
level deep, so it overrides `theme.mode` / `foreColor` / grid color while
preserving each chart's own `chart.type` and other settings. Because
`useApexTheme()` subscribes to the theme context, charts re-render and re-theme
automatically when the mode flips.

Optionally, `web/src/components/charts/base.ts` currently hard-codes
`theme: { mode: "light" }` in `commonOptions`. That value is harmless once each
chart merges `useApexTheme()` last (the hook wins), but you may drop it from
`commonOptions` for clarity. Do NOT rely on editing `base.ts` alone to theme
charts — Apex needs the per-chart merge because most charts build `options`
locally rather than spreading `commonOptions`.

---

## (e) Optional: no-flash inline script in `index.html` `<head>`

To avoid a light→dark flash before React mounts, add this to
`web/index.html` inside `<head>` (before the module script). It defaults to
**dark** and mirrors the provider's read logic:

```html
<script>
  (function () {
    try {
      var t = localStorage.getItem("gakatime-theme");
      if (t === "system") {
        t = window.matchMedia("(prefers-color-scheme: light)").matches
          ? "light"
          : "dark";
      }
      if (t !== "light") t = "dark"; // default to dark
      var root = document.documentElement;
      root.classList.toggle("dark", t === "dark");
      root.style.colorScheme = t;
    } catch (e) {
      document.documentElement.classList.add("dark");
      document.documentElement.style.colorScheme = "dark";
    }
  })();
</script>
```

This is purely a pre-paint optimization; the app works correctly without it
because `ThemeProvider` applies the class on mount.
