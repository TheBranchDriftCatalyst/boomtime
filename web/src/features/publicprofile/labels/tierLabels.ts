// tierLabels.ts — helper that expands ONE tier-config into 5 LabelSpecs
// (novice/apprentice/adept/master/legend), one per tier band. Keeps the
// catalog compact: an axis-value tier ladder is a single call site.
//
// Semantics: at award time, all 5 conditions may match simultaneously
// (a Legend also crosses the Master threshold). The evaluator dedupes
// by `tierKey`, keeping the highest tier reached — see evaluator.ts.
import type { Axis, LabelSpec, LabelTier } from "./types";

const TIER_ORDER: LabelTier[] = [
  "novice",
  "apprentice",
  "adept",
  "master",
  "legend",
];

/** Tier-specific rank offsets — Legend outranks Master, etc. Layered on
 *  top of the caller's base `rank` so hand-authored archetypes can still
 *  outrank a Novice while a Legend still floats to the top of the hero. */
const TIER_RANK_BONUS: Record<LabelTier, number> = {
  novice: 0,
  apprentice: 1,
  adept: 2,
  master: 3,
  legend: 4,
};

export interface TierLabelsInput {
  axis: Axis;
  /** The axis-value (e.g., "python", "vim"). Case-insensitive at match time. */
  value: string;
  /** Display glyph applied to all 5 tier specs (optional). */
  glyph?: string;
  /** Base rank for the ladder. Legend = base + 4, Master = base + 3, etc. */
  rank: number;
  /** Hours thresholds per tier. Must be monotonically non-decreasing. */
  thresholds: Record<LabelTier, number>;
  /** Optional custom label template. `{value}` → axis value uppercased,
   *  `{tier}` → tier name uppercased. Default: "{value} {tier}". */
  labelTemplate?: string;
  /** Optional custom description template. Same substitutions. Default is
   *  "{tier} tier on {value} (≥ {hours}h)". */
  descriptionTemplate?: string;
}

function render(tpl: string, subs: Record<string, string>): string {
  return tpl.replace(/\{(\w+)\}/g, (_, k) => subs[k] ?? "");
}

/** Expand one tier config into 5 LabelSpecs. */
export function tierLabels(input: TierLabelsInput): LabelSpec[] {
  const tpl = input.labelTemplate ?? "{value} {tier}";
  const descTpl =
    input.descriptionTemplate ?? "{tier} tier on {value} (≥ {hours}h)";
  const key = `${input.axis}:${input.value.toLowerCase()}`;
  return TIER_ORDER.map((tier) => {
    const hours = input.thresholds[tier];
    const subs = {
      tier: tier.toUpperCase(),
      value: input.value.toUpperCase(),
      hours: String(hours),
    };
    const spec: LabelSpec = {
      id: `${input.axis}-${input.value.toLowerCase()}-${tier}`,
      kind: "tier",
      tier,
      tierKey: key,
      label: render(tpl, subs),
      glyph: input.glyph,
      description: render(descTpl, subs),
      rank: input.rank + TIER_RANK_BONUS[tier],
      condition: {
        kind: "axis-time",
        axis: input.axis,
        value: input.value,
        op: ">=",
        hours,
      },
    };
    return spec;
  });
}
