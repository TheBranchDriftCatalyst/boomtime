// useLabelsCatalog — react-query fetch hook for the DB-backed labels catalog
// (gaka-364.3). Public endpoint (no auth); the FE evaluator can't award
// anything without it, so every consumer that needs `evaluate(payload)`
// also calls this hook.
//
// Caching: staleTime 60s — labels change rarely (admin CRUD edits are the
// only write path today). Refetch-on-focus disabled: the top-3 hero labels
// don't need to twitch when a user tabs back.
//
// The hook returns the raw wire rows AND a `specs` field pre-converted to
// LabelSpec (the evaluator's input type) so callers don't have to remember
// to run the converter themselves.
import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@shared/lib/api";
import { qk } from "@shared/lib/queryKeys";
import type { LabelCatalogRow, LabelSpec, LabelTier } from "./types";

/** Convert a DB-native catalog row into the LabelSpec shape the evaluator
 *  wants. The only non-trivial bit is `tierKey`: the DB stores `tier` as a
 *  plain string ("novice"/"apprentice"/...) but the evaluator dedupes tier
 *  collisions by (axis-value) — we reconstruct that from the id, which
 *  follows the "{axis}-{value}-{tier}" convention set by tierLabels() in
 *  the original TS catalog. */
export function dbRowToSpec(row: LabelCatalogRow): LabelSpec {
  const spec: LabelSpec = {
    id: row.id,
    kind: row.kind,
    label: row.label,
    glyph: row.glyph || undefined,
    description: row.description,
    rank: row.rank,
    condition: row.condition,
    imagePrompt: row.optimizedPrompt || undefined,
    // gaka-mwp-streaks: pass through per-label period override so
    // useAwardStreaks.resolvePeriod can use it. Empty = kind default.
    periodDefault: row.periodDefault,
  };
  if (row.kind === "tier" && row.tier) {
    spec.tier = row.tier as LabelTier;
    // Reconstruct tierKey from the id. Every tier row's id is
    // `{axis}-{value}-{tier}` (e.g. "languages-python-master"). Strip the
    // trailing tier band to get `{axis}-{value}`, then rewrite the FIRST
    // dash into a colon to match the evaluator's `axis:value` format.
    const withoutTier = row.id.replace(new RegExp(`-${row.tier}$`), "");
    const firstDash = withoutTier.indexOf("-");
    if (firstDash > 0) {
      spec.tierKey =
        withoutTier.slice(0, firstDash) + ":" + withoutTier.slice(firstDash + 1);
    }
  }
  return spec;
}

export function useLabelsCatalog() {
  const query = useQuery({
    queryKey: qk.labelsCatalog(),
    queryFn: () => api.getLabelsCatalog(),
    // 60s stale — matches the server's Cache-Control: max-age=60.
    staleTime: 60_000,
    refetchOnWindowFocus: false,
  });

  const specs = useMemo<LabelSpec[]>(() => {
    if (!query.data) return [];
    return query.data.labels.map(dbRowToSpec);
  }, [query.data]);

  return {
    ...query,
    /** The raw wire rows (used by the admin table which needs `updatedAt`
     *  etc). */
    rows: query.data?.labels ?? [],
    /** Pre-converted evaluator input — pass to `evaluate(payload, {catalog: specs})`. */
    specs,
    /** Global generation systemPrompt — surfaced for the admin editor. */
    systemPrompt: query.data?.systemPrompt ?? "",
  };
}
