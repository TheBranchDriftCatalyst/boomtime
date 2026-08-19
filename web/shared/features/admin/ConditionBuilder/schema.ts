// Zod schema for the label Condition DSL (gaka-6uf). Mirrors the Go
// validator at internal/labels/validate.go — every enum, every required
// field, every numeric range is duplicated here so the FE surfaces
// mistakes before the round-trip. If you touch one, touch the other.
import { z } from "zod";
import type { Condition } from "@shared/features/publicprofile/labels/types";

/** Composer nesting depth cap. Matches internal/labels/validate.go
 *  MaxConditionDepth so an FE tree at the cap always round-trips. */
export const MAX_CONDITION_DEPTH = 5;

const AxisSchema = z.enum([
  "languages",
  "editors",
  "projects",
  "categories",
  "platforms",
]);
export type AxisValue = z.infer<typeof AxisSchema>;

const OpSchema = z.enum([">=", "<="]);

const nonEmptyString = z.string().min(1, "must be non-empty");
const positiveNumber = z.number().positive("must be > 0");
const nonNegativeNumber = z.number().min(0, "must be >= 0");
const pctFraction = z
  .number()
  .min(0, "pct must be in [0, 1]")
  .max(1, "pct must be in [0, 1] — the DSL uses fractions, not 0..100");
const positiveInt = z.number().int().positive("must be a positive integer");

// Recursive builder via z.lazy — Zod cannot infer the type of a self-
// referential union so we `as` a placeholder and rely on the exported
// Condition type from the source-of-truth (types.ts) for consumers.
export const ConditionSchema: z.ZodType<Condition> = z.lazy(() =>
  z.discriminatedUnion("kind", [
    z.object({
      kind: z.literal("axis-time"),
      axis: AxisSchema,
      value: nonEmptyString,
      op: OpSchema,
      hours: positiveNumber,
    }),
    z.object({
      kind: z.literal("axis-time-sum"),
      axis: AxisSchema,
      values: z.array(nonEmptyString).min(1, "at least one value"),
      op: OpSchema,
      hours: positiveNumber,
    }),
    z.object({
      kind: z.literal("axis-pct"),
      axis: AxisSchema,
      value: nonEmptyString,
      op: OpSchema,
      pct: pctFraction,
    }),
    z.object({
      kind: z.literal("top-share"),
      axis: AxisSchema,
      op: OpSchema,
      pct: pctFraction,
    }),
    z.object({
      kind: z.literal("distinct-count"),
      axis: AxisSchema,
      minHoursEach: nonNegativeNumber,
      op: OpSchema,
      n: positiveInt,
    }),
    z.object({
      kind: z.literal("punchcard-hour-pct"),
      hoursIn: z
        .array(z.number().int().min(0).max(23))
        .min(1, "at least one hour"),
      op: OpSchema,
      pct: pctFraction,
    }),
    z.object({
      kind: z.literal("punchcard-dow-pct"),
      dowIn: z
        .array(z.number().int().min(0).max(6))
        .min(1, "at least one day-of-week"),
      op: OpSchema,
      pct: pctFraction,
    }),
    z.object({
      kind: z.literal("streak"),
      which: z.enum(["current", "longest"]),
      op: OpSchema,
      days: positiveInt,
    }),
    z.object({
      kind: z.literal("daily-avg"),
      op: OpSchema,
      hours: positiveNumber,
    }),
    z.object({
      kind: z.literal("trend"),
      window: z.literal("last7-vs-prior7"),
      op: OpSchema,
      ratio: positiveNumber,
    }),
    z.object({
      kind: z.literal("all"),
      of: z.array(ConditionSchema).min(1, "at least one sub-condition"),
    }),
    z.object({
      kind: z.literal("any"),
      of: z.array(ConditionSchema).min(1, "at least one sub-condition"),
    }),
    z.object({
      kind: z.literal("not"),
      of: ConditionSchema,
    }),
  ]) as unknown as z.ZodType<Condition>,
);

/** Discriminator values every primitive kind can take. */
export const PRIMITIVE_KINDS = [
  "axis-time",
  "axis-time-sum",
  "axis-pct",
  "top-share",
  "distinct-count",
  "punchcard-hour-pct",
  "punchcard-dow-pct",
  "streak",
  "daily-avg",
  "trend",
] as const;

export const COMPOSER_KINDS = ["all", "any", "not"] as const;

export type PrimitiveKind = (typeof PRIMITIVE_KINDS)[number];
export type ComposerKind = (typeof COMPOSER_KINDS)[number];

/** Human labels for the kind picker. Keep aligned with formatCondition.ts. */
export const KIND_LABELS: Record<PrimitiveKind | ComposerKind, string> = {
  "axis-time": "Time on axis value",
  "axis-time-sum": "Time summed across N values",
  "axis-pct": "% of axis total",
  "top-share": "Top entry's share",
  "distinct-count": "Distinct entries ≥ minHours",
  "punchcard-hour-pct": "% in hours-of-day",
  "punchcard-dow-pct": "% in days-of-week",
  streak: "Consecutive-day streak",
  "daily-avg": "Daily average hours",
  trend: "Last-7d vs prior-7d ratio",
  all: "AND — every sub-condition",
  any: "OR — at least one sub-condition",
  not: "NOT — negate a sub-condition",
};

/** Depth-cap check — cheap, non-Zod (Zod can't express a runtime depth
 *  limit on a lazy self-reference). Call before round-tripping via Zod. */
export function conditionDepth(c: Condition): number {
  switch (c.kind) {
    case "all":
    case "any":
      return 1 + c.of.reduce((m, sub) => Math.max(m, conditionDepth(sub)), 0);
    case "not":
      return 1 + conditionDepth(c.of);
    default:
      return 0;
  }
}

/** Freshly-constructed default for a given kind. Ensures every field the
 *  Zod schema requires is present so a newly-picked kind doesn't render
 *  the whole builder as "invalid" until the user fills something in. */
export function newCondition(kind: PrimitiveKind | ComposerKind): Condition {
  switch (kind) {
    case "axis-time":
      return { kind, axis: "languages", value: "", op: ">=", hours: 1 };
    case "axis-time-sum":
      return { kind, axis: "editors", values: [""], op: ">=", hours: 1 };
    case "axis-pct":
      return { kind, axis: "languages", value: "", op: ">=", pct: 0.5 };
    case "top-share":
      return { kind, axis: "projects", op: ">=", pct: 0.5 };
    case "distinct-count":
      return { kind, axis: "languages", minHoursEach: 5, op: ">=", n: 3 };
    case "punchcard-hour-pct":
      return { kind, hoursIn: [0, 1, 2, 3, 4, 5], op: ">=", pct: 0.3 };
    case "punchcard-dow-pct":
      return { kind, dowIn: [0, 6], op: ">=", pct: 0.3 };
    case "streak":
      return { kind, which: "current", op: ">=", days: 7 };
    case "daily-avg":
      return { kind, op: ">=", hours: 4 };
    case "trend":
      return { kind, window: "last7-vs-prior7", op: ">=", ratio: 1.2 };
    case "all":
      return { kind, of: [newCondition("axis-time")] };
    case "any":
      return { kind, of: [newCondition("axis-time")] };
    case "not":
      return { kind, of: newCondition("axis-time") };
  }
}

/** Parse a JSON string into a validated Condition. Returns {condition,
 *  error}; error is a compact human message ("/of/0/hours: must be > 0") on
 *  failure. */
export function parseConditionJson(
  text: string,
): { condition: Condition | null; error: string | null } {
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch (e) {
    return {
      condition: null,
      error: e instanceof Error ? e.message : "invalid JSON",
    };
  }
  const result = ConditionSchema.safeParse(parsed);
  if (!result.success) {
    // First issue keeps the error compact; the pointer path helps the user
    // fix one field at a time (same behavior as the server validator).
    const first = result.error.issues[0];
    const path = first.path.length ? "/" + first.path.join("/") + ": " : "";
    return { condition: null, error: path + first.message };
  }
  const cond = result.data as Condition;
  if (conditionDepth(cond) > MAX_CONDITION_DEPTH) {
    return {
      condition: null,
      error: `composer depth exceeds cap (${MAX_CONDITION_DEPTH})`,
    };
  }
  return { condition: cond, error: null };
}

/** Pretty-print a condition as JSON for the raw pane, with stable field
 *  order so a round-trip through the raw pane doesn't shuffle keys under
 *  the user. */
export function formatConditionJson(c: Condition): string {
  return JSON.stringify(c, null, 2);
}
