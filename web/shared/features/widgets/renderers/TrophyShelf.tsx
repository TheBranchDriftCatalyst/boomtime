// TrophyShelf — lazy WebGL boundary for the 3D award medallions (gaka-174.5).
//
// Wraps catalyst-ui's <DossierTrophyShelf> (the R3F coin scene). Only loads
// three/R3F when there are awards to show AND the client supports WebGL and
// hasn't asked to reduce motion — otherwise LabelsShowcase renders its flat
// chip grid instead (progressive enhancement).
import { lazy, Suspense, useState } from "react";
import type { LabelAward } from "@shared/features/publicprofile/labels/types";

const DossierTrophyShelf = lazy(() =>
  import(
    "@thebranchdriftcatalyst/catalyst-ui/components/DossierTrophyShelf"
  ).then((m) => ({ default: m.DossierTrophyShelf })),
);

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

/** True when the 3D shelf can render; LabelsShowcase falls back to chips when
 * false so the info is never lost on no-WebGL / reduced-motion clients. */
export function trophyShelfSupported(): boolean {
  const reduced =
    typeof window !== "undefined" &&
    !!window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
  return supportsWebGL() && !reduced;
}

// Coin tint: prefer the tier ladder, else the label kind.
const TIER_COLOR: Record<string, string> = {
  novice: "#9ca3af",
  apprentice: "#86efac",
  adept: "#93c5fd",
  master: "#c4b5fd",
  legend: "#fbbf24",
};
const KIND_COLOR: Record<string, string> = {
  patch: "#f5a623",
  meme: "#ff4d9d",
  tier: "#e8b23a",
  archetype: "#7dd3fc",
  tribe: "#5eead4",
};
function coinColor(a: LabelAward): string {
  return (a.tier && TIER_COLOR[a.tier]) || KIND_COLOR[a.kind] || "#e8b23a";
}

export function TrophyShelf({ awards }: { awards: LabelAward[] }) {
  const [enabled] = useState(() => trophyShelfSupported());
  if (!enabled || awards.length === 0) return null;

  const items = awards.map((a) => ({
    id: a.id,
    label: a.label,
    glyph: a.glyph,
    color: coinColor(a),
  }));

  return (
    <div className="labels-trophy-shelf" aria-hidden data-testid="trophy-shelf">
      <Suspense fallback={null}>
        <DossierTrophyShelf items={items} />
      </Suspense>
    </div>
  );
}
