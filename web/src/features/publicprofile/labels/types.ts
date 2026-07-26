// types.ts — DSL vocabulary for the labels/memeification framework
// (gaka-364).
//
// The whole idea: adding a new label = one object literal in
// `catalog.ts`. Not one function body — one *literal*. That's only
// possible if the "condition" is data, not code. So `Condition` is a
// small discriminated union of declarative predicates evaluated by
// `conditions.ts`. When the primitives don't cover a new axis, add ONE
// case to the evaluator switch — that's the extension point.
//
// Adjacent design notes:
//   - No arbitrary functions in a LabelSpec — keeps the manifest
//     inspectable, JSON-serializable if we ever need to ship the
//     catalog cross-boundary, and prevents "everyone reaches for a
//     one-off function" drift.
//   - The Axis type deliberately covers only what
//     PublicDashboardPayload carries (no `machines` — public payload
//     strips it).
//   - `op` is `>=` or `<=` — not `>`/`<`. That's on purpose: catalog
//     thresholds like "100 hours" want inclusive semantics ("at 100 you
//     ARE a Master") and matches the way tests are written.
import type { PublicDashboardPayload } from "@/types/stats";

export type LabelTier =
  | "novice"
  | "apprentice"
  | "adept"
  | "master"
  | "legend";

/** Which axis of the public dashboard payload the condition inspects. */
export type Axis =
  | "languages"
  | "editors"
  | "projects"
  | "categories"
  | "platforms";

/** Comparison op used across all threshold conditions. */
export type CmpOp = ">=" | "<=";

// -- Condition primitives ----------------------------------------------------
// Each kind is a tiny declarative predicate over the payload. `evaluateCondition`
// (conditions.ts) is a pure switch over the `kind` tag.
//
// Guidelines for adding a new primitive:
//   1. Add the discriminant here.
//   2. Add ONE case to the switch in `conditions.ts`.
//   3. Add a unit test in `conditions.test.ts` exercising both true and
//      false cases with values just above / just below the threshold.
//
// Prefer composition (`all`/`any`/`not`) over new primitives when the
// existing set covers the shape.

/** True when axis-value's total hours crosses the threshold. */
export interface AxisTimeCond {
  kind: "axis-time";
  axis: Axis;
  value: string;
  op: CmpOp;
  hours: number;
}

/** True when axis-value's percentage of the axis total crosses the threshold. */
export interface AxisPctCond {
  kind: "axis-pct";
  axis: Axis;
  value: string;
  op: CmpOp;
  pct: number; // 0..1
}

/** True when the TOP entry on an axis holds ≥/≤ pct of that axis's total. */
export interface TopShareCond {
  kind: "top-share";
  axis: Axis;
  op: CmpOp;
  pct: number; // 0..1
}

/** True when N distinct entries on an axis each carry ≥ minHoursEach hours. */
export interface DistinctCountCond {
  kind: "distinct-count";
  axis: Axis;
  minHoursEach: number;
  op: CmpOp;
  n: number;
}

/** True when a subset of hours-of-day accounts for ≥/≤ pct of punchcard. */
export interface PunchcardHourPctCond {
  kind: "punchcard-hour-pct";
  hoursIn: number[]; // 0..23
  op: CmpOp;
  pct: number; // 0..1
}

/** True when a subset of days-of-week accounts for ≥/≤ pct of punchcard.
 *  DOW numbering matches the payload: 0=Sun..6=Sat. */
export interface PunchcardDowPctCond {
  kind: "punchcard-dow-pct";
  dowIn: number[]; // 0..6
  op: CmpOp;
  pct: number; // 0..1
}

/** True when current/longest streak length crosses the threshold. */
export interface StreakCond {
  kind: "streak";
  which: "current" | "longest";
  op: CmpOp;
  days: number;
}

/** True when dailyAvg (in seconds → hours) crosses the threshold. */
export interface DailyAvgCond {
  kind: "daily-avg";
  op: CmpOp;
  hours: number;
}

/** True when the ratio of the last 7 days' average to the prior 7 days'
 *  average crosses `ratio`. Sprinter-detector for "heating up" archetype. */
export interface TrendCond {
  kind: "trend";
  window: "last7-vs-prior7";
  op: CmpOp;
  ratio: number;
}

// Composition primitives — Boolean algebra over other conditions.
export interface AllCond {
  kind: "all";
  of: Condition[];
}
export interface AnyCond {
  kind: "any";
  of: Condition[];
}
export interface NotCond {
  kind: "not";
  of: Condition;
}

export type Condition =
  | AxisTimeCond
  | AxisPctCond
  | TopShareCond
  | DistinctCountCond
  | PunchcardHourPctCond
  | PunchcardDowPctCond
  | StreakCond
  | DailyAvgCond
  | TrendCond
  | AllCond
  | AnyCond
  | NotCond;

// -- Label specs & awards ----------------------------------------------------

/** Manifest entry — one label. */
export interface LabelSpec {
  /** Stable slug (e.g., "python-master", "late-night-coder"). Used for
   *  dedupe + as a React key. */
  id: string;
  /** Category — drives grouping in the showcase widget and rank preference
   *  in the hero-tagline top-N.
   *
   *  Kinds:
   *    - "tier"      — 5-band ladder per axis-value (novice → legend).
   *    - "archetype" — personality trait; you can hold several at once.
   *    - "tribe"     — community identity (editor/OS allegiance).
   *    - "meme"      — the OP shiznit (gaka-364.1) — memecore / kawaii /
   *      space-marine / sigma-grindset flavor. Ranks intentionally OUTRANK
   *      archetypes so they win the hero top-3 slot when they fire.
   */
  kind: "tier" | "archetype" | "tribe" | "meme";
  /** Display label. Uppercased at render; keep short. */
  label: string;
  /** Optional 1-3 char glyph (emoji or symbol). Purely cosmetic. */
  glyph?: string;
  /** One-line explainer for the showcase tooltip. */
  description: string;
  /** Display priority: higher shows first; used for tie-breaking + top-N. */
  rank: number;
  /** The declarative predicate. */
  condition: Condition;
  /** For tier labels, which tier this spec represents. `tierLabels()` fills
   *  this in for its outputs; hand-written archetype/tribe specs leave it
   *  undefined. Drives the dedupe-keeping-highest step in the evaluator. */
  tier?: LabelTier;
  /** For tier labels, the axis-value pair the tier tracks — used to detect
   *  collisions among the 5 tier specs generated per axis-value so only the
   *  highest reached is awarded. */
  tierKey?: string; // e.g. "languages:python"
  /** Optional prompt template for the ComfyUI label-image worker (gaka-myv).
   *  When set, the worker will render an emblem image for this label and
   *  serve it in place of the glyph. Kept next to the spec so authoring a
   *  new label carries its own art brief. When absent, the frontend falls
   *  back to the glyph.
   *
   *  Legacy: pre-gaka-364.3 this was the ONLY prompt field. Post-pivot, the
   *  server's `optimizedPrompt` column is authoritative; the FE type keeps
   *  imagePrompt as an alias so evaluator tests can continue using inline
   *  fixture specs without needing the DB catalog wire format. See the
   *  `LabelCatalogRow` type for the DB-native shape. */
  imagePrompt?: string;
}

/** Wire shape returned by GET /api/v1/labels/catalog (gaka-364.3). The rows
 *  are convertible to LabelSpec for the evaluator via `dbRowToSpec`. */
export interface LabelCatalogRow {
  id: string;
  kind: LabelSpec["kind"];
  label: string;
  glyph: string;
  description: string;
  optimizedPrompt: string;
  rank: number;
  tier: string; // may be "" when kind !== "tier"
  condition: Condition;
  createdAt: string;
  updatedAt: string;
}

/** Full catalog payload — labels + the singleton generation-config
 *  systemPrompt. */
export interface LabelsCatalogPayload {
  systemPrompt: string;
  labels: LabelCatalogRow[];
}

/** One label awarded to a payload. Consumed by the hero tagline + the
 *  labels-showcase widget. */
export interface LabelAward {
  id: string;
  kind: LabelSpec["kind"];
  label: string;
  glyph?: string;
  description: string;
  rank: number;
  tier?: LabelTier;
}

/** Convenience alias — the evaluator reads the same shape everywhere. */
export type LabelPayload = PublicDashboardPayload;
