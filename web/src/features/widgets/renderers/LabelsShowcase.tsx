// LabelsShowcase — dashboard widget that renders ALL labels awarded by
// the framework (gaka-364), grouped by category (tier / archetype /
// tribe). Complements the top-3 slice shown on the hero tagline.
//
// Purely display over `evaluate(data)`. No fetching — the outer
// DashboardGrid already handed us the PublicDashboardPayload.
import type { PublicDashboardPayload } from "@/types/stats";
import { evaluate } from "@/features/publicprofile/labels/evaluator";
import type { LabelAward } from "@/features/publicprofile/labels/types";
import { LabelImage } from "@/features/publicprofile/labels/LabelImage";

// gaka-364.1: "meme" group renders FIRST so the OP shiznit lands top of the
// showcase widget the same way it lands top of the hero tagline. Rank-desc
// sort inside the group is inherited from the evaluator, we just group here.
const GROUP_ORDER: Array<LabelAward["kind"]> = [
  "meme",
  "tier",
  "archetype",
  "tribe",
];
const GROUP_HEADERS: Record<LabelAward["kind"], string> = {
  meme: "OP Shiznit",
  tier: "Tiers",
  archetype: "Archetypes",
  tribe: "Tribes",
};

export interface LabelsShowcaseProps {
  data: PublicDashboardPayload;
}

export function LabelsShowcase({ data }: LabelsShowcaseProps) {
  const awards = evaluate(data);

  if (awards.length === 0) {
    return (
      <div className="flex h-full w-full items-center justify-center px-3 text-center font-mono text-[11px] uppercase tracking-[0.14em] text-[color:var(--muted-foreground)]">
        NO LABELS YET · KEEP CODING
      </div>
    );
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
                  <span
                    title={a.description}
                    className="inline-flex items-center gap-1 rounded-sm border border-[color:var(--primary)]/40 bg-[color:var(--primary)]/10 px-2 py-1 font-mono text-[10px] uppercase tracking-[0.12em]"
                    data-testid="label-award"
                    data-label-id={a.id}
                  >
                    {/* gaka-myv: image if available, glyph fallback on 404 */}
                    <LabelImage
                      id={a.id}
                      size={16}
                      className="opacity-90"
                      fallback={
                        a.glyph ? (
                          <span aria-hidden className="opacity-80">
                            {a.glyph}
                          </span>
                        ) : null
                      }
                    />
                    <span>{a.label}</span>
                  </span>
                </li>
              ))}
            </ul>
          </section>
        );
      })}
    </div>
  );
}
