// LabelsShowcase — dashboard widget that renders ALL labels awarded by
// the framework (boom-364), grouped by category (tier / archetype /
// tribe). Complements the top-3 slice shown on the hero tagline.
//
// boom-hc6.4: awards now come from the server via useAwards() — no more
// client-side evaluate(). The server also writes ledger rows on its own,
// so the previous useLogAwards() POST is gone (double-write eliminated).
// `data` is still accepted to keep the widget renderer contract stable,
// but the label set no longer depends on it.
import type { PublicDashboardPayload } from "@shared/types/stats";
import { useAwards } from "@shared/features/publicprofile/labels/useAwards";
import { useAwardStreaks } from "@shared/features/publicprofile/labels/useAwardStreaks";
import type { LabelAward } from "@shared/features/publicprofile/labels/types";
import { LabelChip } from "@shared/features/publicprofile/labels/LabelChip";
import { TrophyShelf, trophyShelfSupported } from "@shared/features/widgets/renderers/TrophyShelf";
import { useFeatureFlag } from "@shared/lib/featureFlags";
import "./TrophyShelf.css";

// boom-364.1: "meme" group renders FIRST so the OP shiznit lands top of the
// showcase widget the same way it lands top of the hero tagline. Rank-desc
// sort inside the group is inherited from the evaluator, we just group here.
const GROUP_ORDER: Array<LabelAward["kind"]> = [
  "patch",
  "meme",
  "tier",
  "archetype",
  "tribe",
];
const GROUP_HEADERS: Record<LabelAward["kind"], string> = {
  patch: "Patches",
  meme: "OP Shiznit",
  tier: "Tiers",
  archetype: "Archetypes",
  tribe: "Tribes",
};

export interface LabelsShowcaseProps {
  // kept in the signature for renderer-contract stability; awards no
  // longer depend on it (server evaluates against the caller/slug's
  // authoritative payload).
  data: PublicDashboardPayload;
}

export function LabelsShowcase(_: LabelsShowcaseProps) {
  // boom-174.5: the 3D medallion shelf is opt-in behind a feature flag; the
  // classic chip grid is the default. Flip it on via the dossier flipper.
  const [labels3D] = useFeatureFlag("labels3D");
  const awards = useAwards();
  // Streaks map still comes from its own endpoint — the LabelChip needs
  // it to render Nx badges. Server-side ledger writes on /awards mean
  // the streak count reflects the just-computed period automatically.
  const streaks = useAwardStreaks();

  if (awards.length === 0) {
    return (
      <div className="flex h-full w-full items-center justify-center px-3 text-center font-mono text-[11px] uppercase tracking-[0.14em] text-[color:var(--muted-foreground)]">
        NO LABELS YET · KEEP CODING
      </div>
    );
  }

  // boom-174.5: 3D medallions only when the viewer has flipped the flag ON
  // AND the client supports WebGL. Otherwise fall through to the classic chip
  // grid (the default), which also keeps the hover-tooltip descriptions.
  if (labels3D && trophyShelfSupported()) {
    return <TrophyShelf awards={awards} />;
  }

  const groups = new Map<LabelAward["kind"], LabelAward[]>();
  for (const a of awards) {
    const bucket = groups.get(a.kind) ?? [];
    bucket.push(a);
    groups.set(a.kind, bucket);
  }

  return (
    <div
      className="flex h-full w-full flex-col gap-3 overflow-y-auto p-3"
      data-testid="labels-showcase"
    >
      {GROUP_ORDER.filter((k) => groups.has(k)).map((k) => {
        const list = groups.get(k)!;
        return (
          <section key={k} className="flex flex-col gap-1.5">
            <header className="flex items-baseline justify-between font-mono text-[10px] uppercase tracking-[0.2em] text-[color:var(--muted-foreground)]">
              <span>{GROUP_HEADERS[k]}</span>
              <span className="tabular-nums">{list.length}</span>
            </header>
            <ul className="flex flex-wrap gap-1.5">
              {list.map((a) => (
                <li key={a.id}>
                  {/*
                    boom-mem-chip: LabelChip owns the full chip chrome —
                    image or glyph + hover tooltip with the achievement's
                    description. Replaces the earlier native title=... tip
                    which browsers style inconsistently and can't show an image.
                  */}
                  <LabelChip award={a} size="md" streak={streaks[a.id]} />
                </li>
              ))}
            </ul>
          </section>
        );
      })}
    </div>
  );
}
