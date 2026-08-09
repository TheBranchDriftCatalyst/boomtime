// WidgetRenderer — dispatches a widget kind id to the in-page React
// renderer for the composable dashboard grid (gaka-keb).
//
// Every kind that opts into `dashboardScopes: ['profile']` must have an
// entry here. Unknown / unrendered kinds fall through to a small
// placeholder — the DashboardGrid also filters upstream so this branch
// mostly guards against catalog-renderer drift.
import type { PublicDashboardPayload } from "@/types/stats";
// Part B Stage 3 (gaka-174.x) built the data-driven alternative to a
// hand-written switch case per target:"both" kind, gated behind the
// widgetSpecEngine FE flag. Part B Stage 5 cutover: the flag is gone —
// EVERY target:"both" kind now routes through SpecRenderer unconditionally.
// This file's switch below is left with only the fe-only kinds (hero tile,
// grade badge, labels showcase, github-stats), which have no spec panels to
// dispatch and always stay bespoke.
//
// Part B Stage 4: the goal-* kinds are target:"both" (privacy-gated
// embeddable SVGs too), and SpecRenderer dispatches them to the SAME
// self-fetching GoalProgress/GoalRing/GoalList components this file used to
// case on directly — so in-page goal rendering is unchanged, just routed
// through SpecRenderer now.
import { specForKind } from "@/features/widgets/specs";
import { SpecRenderer } from "@/features/widgets/renderers/SpecRenderer";
import { HoloGradeBadge } from "@/features/widgets/renderers/HoloGradeBadge";
// gaka-364: label evaluator drives the hero tagline (top-3 awards) +
// the labels-showcase widget. gaka-hc6.4: awards come from the server
// via useAwards() — no more client-side evaluate(), no more client POST
// to /awards/log (server writes the ledger atomically on its own read).
// While the fetch is in flight useAwards returns [] and the fallback
// "NEW OPERATOR" branch renders — same UX as an actually-empty award set.
import { useAwards } from "@/features/publicprofile/labels/useAwards";
import { useAwardStreaks } from "@/features/publicprofile/labels/useAwardStreaks";
import { LabelChip } from "@/features/publicprofile/labels/LabelChip";
import { LabelsShowcase } from "@/features/widgets/renderers/LabelsShowcase";
// gaka-9v4: per-user chibi avatar slot in the hero identity tile. Falls
// back to initials when the user hasn't rendered one yet — the profile
// still reads cleanly for the "new operator" case.
import { UserAvatarImage } from "@/features/publicprofile/UserAvatarImage";
// gaka-2ud P5: the public GitHub stats tile. Self-fetches the UNAUTH public
// mirror by slug and self-hides on 404 / empty — no CTA, no error on the
// public page. FE-only (no SVG variant — no spec panels).
import { GithubCard } from "@/features/publicprofile/GithubCard";

interface Ctx {
  view?: string;
  width?: number;
  height?: number;
}

export interface WidgetRendererProps {
  kind: string;
  view?: string;
  data: PublicDashboardPayload;
  /** gaka-2ud P5: the public profile slug. Threaded from PublicDashboard /
   * ProfileEditor so slug-scoped kinds (github-stats) can fetch the public
   * mirror. Falls back to `data.username` for callers that don't pass it. */
  slug?: string;
  ctx?: Ctx;
}

export function WidgetRenderer({ kind, view, data, slug, ctx }: WidgetRendererProps) {
  const height = ctx?.height ?? 220;

  // Part B Stage 5 cutover: every target:"both" kind routes through the
  // generic SpecRenderer unconditionally — no more flag check.
  if (specForKind(kind)?.target === "both") {
    return <SpecRenderer kind={kind} view={view} data={data} height={height} />;
  }

  switch (kind) {
    case "hero-identity":
      return <HeroIdentity data={data} />;

    case "grade-badge":
      return <HoloGradeBadge data={data} />;

    // gaka-364: labels showcase — all awarded labels grouped by kind
    case "labels-showcase":
      return <LabelsShowcase data={data} />;

    // gaka-2ud P5: public GitHub stats. Needs the SLUG to fetch the unauth
    // public mirror; falls back to the payload's username. Renders nothing
    // when there's no public GitHub data (no CTA on the public page).
    case "github-stats":
      return <GithubCard slug={slug ?? data.username} />;

    default:
      return <Empty note={`No renderer for "${kind}"`} />;
  }
}

function HeroIdentity({ data }: { data: PublicDashboardPayload }) {
  // gaka-364: hero tagline is the top-3 awarded labels from the memeification
  // catalog. Fallback text when there are no awards at all — deliberately
  // unambiguous ("NEW OPERATOR" signals "we've got no data on you" more
  // clearly than the old POLYGLOT-CLASS placeholder ever did).
  //
  // gaka-mem-chip: the previous split of "plain-text tagline row" +
  // "separate emblem row of naked <img>s" collapsed into ONE row of
  // <LabelChip>s. Each chip carries its own image + hover tooltip with
  // the description of what the label means. No duplication.
  const awards = useAwards();
  // Read streak counts so hero chips show "Nx" badges when a label has
  // recurred. Server-side ledger write on /awards keeps this fresh.
  const streaks = useAwardStreaks();
  const top3 = awards.slice(0, 3);
  return (
    <div
      className="flex h-full items-center px-3"
      style={{ gap: 16 }}
      data-testid="hero-identity"
    >
      {/* gaka-9v4: chibi avatar square. Falls back to initials in an
       *  amber-bordered square when the user hasn't rendered one — the
       *  hero still reads cleanly in the "new operator" empty state. */}
      <div className="shrink-0" data-testid="hero-avatar-slot">
        <UserAvatarImage username={data.username} size={72} />
      </div>
      <div className="flex flex-col justify-center min-w-0">
        <div className="mb-1 font-mono text-[10px] uppercase tracking-[0.18em] text-[color:var(--muted-foreground)]">
          &gt; PROFILE · {data.username}@boomtime
        </div>
        <div
          className="font-mono text-4xl font-bold uppercase leading-none tracking-tight text-[color:var(--primary)] truncate"
          style={{ textShadow: "0 0 20px var(--primary)" }}
        >
          {data.username}
        </div>
        <div className="mt-2 flex items-center gap-3">
          <span
            className="inline-block h-[2px] w-16 shrink-0"
            style={{ background: "var(--primary)" }}
            aria-hidden
          />
          {top3.length === 0 ? (
            <span
              className="font-mono text-[10px] uppercase tracking-[0.2em] text-[color:var(--accent,var(--primary))]"
              data-testid="hero-tagline"
            >
              NEW OPERATOR
            </span>
          ) : (
            <div
              className="flex flex-wrap items-center gap-1.5"
              data-testid="hero-tagline"
            >
              {top3.map((a) => (
                <LabelChip key={a.id} award={a} size="sm" streak={streaks[a.id]} />
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

function Empty({ note }: { note: string }) {
  return (
    <div className="flex h-full w-full items-center justify-center font-mono text-[11px] uppercase tracking-[0.15em] text-[color:var(--muted-foreground)]">
      {note}
    </div>
  );
}
