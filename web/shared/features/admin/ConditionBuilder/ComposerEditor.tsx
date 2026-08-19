// ComposerEditor — bordered-card renderer for the three composers
// (all/any/not). Mirrors the group-node shape from PredicateBuilder
// (features/goals/PredicateBuilder.tsx): operator select at the top,
// indented children below, add/remove/convert affordances.
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Plus, X } from "lucide-react";
import type { Condition, AllCond, AnyCond, NotCond } from "@shared/features/publicprofile/labels/types";
import { ConditionBuilder } from "./index";
import { MAX_CONDITION_DEPTH, newCondition, type ComposerKind } from "./schema";

interface Props {
  value: AllCond | AnyCond | NotCond;
  onChange: (next: Condition) => void;
  depth: number;
}

/** Renders a composer card. `depth` propagates so the "add sub-condition"
 *  affordance can disable itself at the cap without needing a global
 *  context. */
export function ComposerEditor({ value, onChange, depth }: Props) {
  const atCap = depth >= MAX_CONDITION_DEPTH;

  return (
    <div className="rounded-md border border-border bg-muted/30 p-2">
      <div className="mb-2 flex items-center gap-2">
        <select
          className="h-7 rounded-md border border-input bg-background px-2 font-mono text-[11px] uppercase tracking-wide"
          value={value.kind}
          onChange={(e) => onChange(convertComposer(value, e.target.value as ComposerKind))}
        >
          <option value="all">ALL</option>
          <option value="any">ANY</option>
          <option value="not">NOT</option>
        </select>
        <span className="font-mono text-[10px] text-muted-foreground">
          depth {depth + 1}/{MAX_CONDITION_DEPTH}
        </span>
        <div className="ml-auto flex gap-1">
          <Button
            type="button"
            size="sm"
            variant="ghost"
            className="h-7 px-2 text-[11px]"
            onClick={() => {
              // "Unwrap" — replace the composer with its first (or only) child.
              // For `not`, unwrap to its `of`; for `all`/`any`, unwrap to
              // the first sub-condition. Handy for backing out of a group.
              if (value.kind === "not") onChange(value.of);
              else if (value.of.length > 0) onChange(value.of[0]);
            }}
          >
            Unwrap
          </Button>
        </div>
      </div>

      {value.kind === "not" ? (
        <div className="pl-3">
          <ConditionBuilder
            value={value.of}
            onChange={(next) => onChange({ ...value, of: next })}
            depth={depth + 1}
          />
        </div>
      ) : (
        <div className="space-y-2 pl-3">
          {value.of.map((sub, i) => (
            <div key={i} className="flex items-start gap-2">
              <div className="flex-1">
                <ConditionBuilder
                  value={sub}
                  onChange={(next) => onChange(replaceAt(value, i, next))}
                  depth={depth + 1}
                />
              </div>
              <Button
                type="button"
                size="sm"
                variant="ghost"
                className="mt-1 h-7 w-7 p-0"
                onClick={() => onChange(removeAt(value, i))}
                disabled={value.of.length <= 1}
                title={
                  value.of.length <= 1
                    ? "Can't remove the last child — Unwrap the group instead"
                    : "Remove"
                }
              >
                <X className="h-3 w-3" />
              </Button>
            </div>
          ))}
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="h-7 text-[11px]"
            disabled={atCap}
            onClick={() =>
              onChange({
                ...value,
                of: [...value.of, newCondition("axis-time")],
              })
            }
            title={atCap ? `Depth cap ${MAX_CONDITION_DEPTH} reached` : "Add sub-condition"}
          >
            <Plus className="mr-1 h-3 w-3" />
            Add sub-condition
          </Button>
        </div>
      )}
    </div>
  );
}

/** Convert between composer kinds while preserving as much of the current
 *  tree as possible: all↔any keeps the `of` array; either → not takes the
 *  first child; not → all/any wraps the single `of`. */
function convertComposer(
  cur: AllCond | AnyCond | NotCond,
  next: ComposerKind,
): AllCond | AnyCond | NotCond {
  if (cur.kind === next) return cur;
  if (next === "not") {
    if (cur.kind === "not") return cur;
    // Drop the tail — `not` takes a single sub-condition.
    return { kind: "not", of: cur.of[0] ?? newCondition("axis-time") };
  }
  if (cur.kind === "not") {
    return { kind: next, of: [cur.of] };
  }
  // all ↔ any: swap kind, keep children.
  return { kind: next, of: cur.of };
}

function replaceAt(
  cur: AllCond | AnyCond,
  i: number,
  next: Condition,
): AllCond | AnyCond {
  const of = cur.of.slice();
  of[i] = next;
  return { ...cur, of };
}

function removeAt(cur: AllCond | AnyCond, i: number): AllCond | AnyCond {
  return { ...cur, of: cur.of.filter((_, idx) => idx !== i) };
}
