// HeroBackdrop — the lazy WebGL boundary for the dossier hero (boom-174.3).
//
// Wraps catalyst-ui's <DossierHeroBackdrop> (the actual R3F scene) so that:
//   - three + R3F only download when a profile is opened AND the client can
//     actually use them (React.lazy → separate chunk, gated below).
//   - no-WebGL / prefers-reduced-motion clients render NOTHING here — the hero
//     is fully legible without the backdrop, so the fallback is simply its
//     absence (progressive enhancement, never a broken box).
//   - the field is tinted with the live theme's --primary, re-resolved on
//     every theme (dossier-skin) change.
import { lazy, Suspense, useEffect, useState } from "react";
import { useTheme } from "@thebranchdriftcatalyst/catalyst-ui/contexts/Theme";

// Lazy so the three/R3F runtime is a route-split chunk, never in the main
// bundle. Only owners/visitors who reach a WebGL-capable profile fetch it.
const DossierHeroBackdrop = lazy(() =>
  import(
    "@thebranchdriftcatalyst/catalyst-ui/components/DossierHeroBackdrop"
  ).then((m) => ({ default: m.DossierHeroBackdrop })),
);

// WebGL capability probe — cached at module scope so we don't spin up a
// throwaway GL context on every mount.
let webglSupported: boolean | null = null;
function supportsWebGL(): boolean {
  if (webglSupported !== null) return webglSupported;
  try {
    const c = document.createElement("canvas");
    webglSupported = !!(
      window.WebGLRenderingContext &&
      (c.getContext("webgl") || c.getContext("experimental-webgl"))
    );
  } catch {
    webglSupported = false;
  }
  return webglSupported;
}

function prefersReducedMotion(): boolean {
  return (
    typeof window !== "undefined" &&
    !!window.matchMedia?.("(prefers-reduced-motion: reduce)").matches
  );
}

// CSS custom props may be oklch/oklab, which THREE.Color won't parse. Resolve
// --primary through a hidden probe so the browser hands back a concrete
// rgb(...) string the shader can consume.
function resolvePrimary(): string {
  try {
    const probe = document.createElement("span");
    probe.style.color = "var(--primary)";
    probe.style.display = "none";
    document.body.appendChild(probe);
    const rgb = getComputedStyle(probe).color;
    probe.remove();
    return rgb || "rgb(242,114,200)";
  } catch {
    return "rgb(242,114,200)";
  }
}

export function HeroBackdrop() {
  const { theme } = useTheme();
  // Decide once per mount — capability + motion preference don't change
  // mid-session in any way we need to react to.
  const [enabled] = useState(
    () => supportsWebGL() && !prefersReducedMotion(),
  );
  const [color, setColor] = useState<string>("rgb(242,114,200)");

  useEffect(() => {
    if (!enabled) return;
    // rAF so the theme's CSS vars are committed before we sample them.
    const id = requestAnimationFrame(() => setColor(resolvePrimary()));
    return () => cancelAnimationFrame(id);
  }, [theme, enabled]);

  if (!enabled) return null;

  return (
    <div className="public-dashboard__hero-backdrop" aria-hidden>
      <Suspense fallback={null}>
        <DossierHeroBackdrop color={color} intensity={0.9} />
      </Suspense>
    </div>
  );
}
