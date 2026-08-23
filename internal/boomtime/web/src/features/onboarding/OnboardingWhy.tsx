import type { ReactNode } from "react";
import {
  Boxes,
  Braces,
  Download,
  Gauge,
  HeartPulse,
  Plug,
  Rocket,
  Trophy,
} from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";

// The "Why boomtime, not Wakatime?" step (boom-93f.1.2). Grounded in WHY.md +
// QUERY_ENGINE.md + the gamification doc — real differentiators, not marketing
// fluff: the gap_seconds-at-ingest query engine, the reversible 3-strategy
// curation DSL, the earned-gamification layer, the one-button importer, and the
// catalyst-development framing. Closes with a roadmap strip.

interface Diff {
  icon: typeof Gauge;
  title: string;
  body: ReactNode;
  accent: number; // --chart-N index for the icon tint
}

const DIFFS: Diff[] = [
  {
    icon: Gauge,
    accent: 0,
    title: "Instant, even at “All time”",
    body: (
      <>
        Duration is precomputed at ingest (<code>gap_seconds</code>) and backed by a
        daily rollup — so 440k heartbeats over two years render with no spinner.
        Wakatime hides deep history behind its plan.
      </>
    ),
  },
  {
    icon: Braces,
    accent: 3,
    title: "A reversible curation DSL",
    body: (
      <>
        <b>exact</b> · <b>regex</b> · <b>template</b> (capture→replace) rules, applied
        at <i>query time</i>. Rename or hide across your whole history; raw heartbeats
        are never mutated. Delete a rule — it reverts instantly.
      </>
    ),
  },
  {
    icon: Trophy,
    accent: 1,
    title: "Gamification you actually earned",
    body: (
      <>
        Labels, tiers, patches &amp; streaks awarded from your real stats via a
        13-primitive rule DSL + an append-only ledger. Your coding history becomes a
        game you already played.
      </>
    ),
  },
  {
    icon: Download,
    accent: 5,
    title: "One-button migration",
    body: (
      <>
        A durable, resumable, idempotent Wakatime importer. Pull years off
        wakatime.com, point <code>~/.wakatime.cfg</code> here, cancel the subscription.
      </>
    ),
  },
  {
    icon: Boxes,
    accent: 8,
    title: "Part of catalyst-development",
    body: (
      <>
        Composable UI, query, and now auth + payments modules — built by agentic
        engineering, verified live against the real dataset, not synthetic mocks.
      </>
    ),
  },
];

const ROADMAP = [
  { icon: HeartPulse, label: "Health tracking" },
  { icon: Plug, label: "Smart integrations" },
  { icon: Rocket, label: "and more" },
];

export function WhyStep({ onBack, onNext }: { onBack: () => void; onNext: () => void }) {
  return (
    <div className="space-y-5">
      <div className="space-y-1 text-center">
        <h2 className="text-lg font-semibold">Why boomtime, not Wakatime?</h2>
        <p className="text-sm text-muted-foreground">
          A faster, more honest tracker — with superpowers the paid one doesn&apos;t have.
        </p>
      </div>

      <ul className="grid gap-3 sm:grid-cols-2">
        {DIFFS.map((d) => (
          <li
            key={d.title}
            className="rounded-lg border border-border/60 bg-muted/20 p-3"
          >
            <div className="mb-1.5 flex items-center gap-2">
              <span
                className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md"
                style={{
                  background: `color-mix(in oklab, var(--chart-${d.accent + 1}) 18%, transparent)`,
                  color: `var(--chart-${d.accent + 1})`,
                }}
              >
                <d.icon className="h-3.5 w-3.5" />
              </span>
              <span className="text-sm font-semibold leading-tight">{d.title}</span>
            </div>
            <p className="text-xs leading-relaxed text-muted-foreground [&_code]:rounded [&_code]:bg-muted/60 [&_code]:px-1 [&_code]:text-[10px] [&_b]:text-foreground/90">
              {d.body}
            </p>
          </li>
        ))}
      </ul>

      {/* Roadmap strip */}
      <div className="rounded-lg border border-primary/25 bg-primary/5 p-3">
        <div className="mb-2 text-[10px] font-semibold uppercase tracking-wider text-primary/80">
          On the roadmap
        </div>
        <div className="flex flex-wrap gap-x-4 gap-y-2">
          {ROADMAP.map((r) => (
            <span key={r.label} className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <r.icon className="h-3.5 w-3.5 text-primary/70" />
              {r.label}
            </span>
          ))}
        </div>
      </div>

      <div className="flex gap-2">
        <Button variant="secondary" className="flex-1" onClick={onBack}>
          Back
        </Button>
        <Button className="flex-1" onClick={onNext}>
          See what you can do
        </Button>
      </div>
    </div>
  );
}
