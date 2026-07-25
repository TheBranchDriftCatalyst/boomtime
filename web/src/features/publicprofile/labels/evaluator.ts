// evaluator.ts — walks the catalog against a payload, awards passing
// labels, dedupes tier collisions (keeps highest tier per axis-value),
// sorts by rank. Pure function; no state; no time; no fetches.
import type { LabelAward, LabelPayload, LabelSpec, LabelTier } from "./types";
import { evaluateCondition } from "./conditions";
import { LABEL_CATALOG } from "./catalog";

// Ordering used to compare tiers when deduping (Legend > Master > ...).
const TIER_STRENGTH: Record<LabelTier, number> = {
  novice: 0,
  apprentice: 1,
  adept: 2,
  master: 3,
  legend: 4,
};

export interface EvaluateOptions {
  /** Optional catalog override — defaults to the shipped LABEL_CATALOG. Kept
   *  as a parameter so tests can seed a small, deterministic manifest without
   *  monkey-patching the module. */
  catalog?: LabelSpec[];
}

export function evaluate(
  payload: LabelPayload,
  opts: EvaluateOptions = {},
): LabelAward[] {
  // Default catalog = the app's shipped manifest. Tests pass their own to
  // avoid coupling to the moving default.
  const catalog = opts.catalog ?? LABEL_CATALOG;

  // Pass 1: filter to specs whose condition holds on this payload.
  const passing = catalog.filter((s) => evaluateCondition(s.condition, payload));

  // Pass 2: for tier specs sharing a tierKey, keep only the highest-tier one.
  // Non-tier specs (archetype / tribe) pass through unchanged.
  const byTierKey = new Map<string, LabelSpec>();
  const nonTier: LabelSpec[] = [];
  for (const s of passing) {
    if (s.kind === "tier" && s.tierKey) {
      const cur = byTierKey.get(s.tierKey);
      if (!cur) {
        byTierKey.set(s.tierKey, s);
        continue;
      }
      const curT = cur.tier ? TIER_STRENGTH[cur.tier] : -1;
      const newT = s.tier ? TIER_STRENGTH[s.tier] : -1;
      if (newT > curT) byTierKey.set(s.tierKey, s);
    } else {
      nonTier.push(s);
    }
  }

  const winners: LabelSpec[] = [...byTierKey.values(), ...nonTier];

  // Pass 3: sort by rank desc (higher = shown first). Stable secondary by id
  // for deterministic order in tests.
  winners.sort((a, b) => {
    if (b.rank !== a.rank) return b.rank - a.rank;
    return a.id.localeCompare(b.id);
  });

  return winners.map((s) => ({
    id: s.id,
    kind: s.kind,
    label: s.label,
    glyph: s.glyph,
    description: s.description,
    rank: s.rank,
    tier: s.tier,
  }));
}

