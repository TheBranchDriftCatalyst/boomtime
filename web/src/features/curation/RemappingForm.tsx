import { useEffect, useMemo, useState } from "react";
import { ArrowRight, Check, Plus } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { AxisSelect } from "@/features/rules/AxisSelect";
import { AxisValueField } from "@/features/rules/AxisValueField";
import { MatchTypeToggle } from "@/features/rules/MatchTypeToggle";
import {
  MatchPreviewContainer,
  MatchPreviewList,
} from "@/features/rules/MatchPreviewList";
import { CURATABLE_AXES } from "@/features/rules/axes";
import { axisLabel } from "@/lib/axes";
import { useAxisValues } from "@/features/rules/useAxisValues";
import { useCurationMutations } from "@/features/curation/useCuration";
import { templateToBackend, templateToJs } from "@/features/curation/remapDisplay";
import { cn } from "@/lib/utils";
import type { CurationMatchType, HeartbeatAxis } from "@/types/api";

type Mode = CurationMatchType; // "exact" | "regex" | "template"

const MODES: readonly Mode[] = ["exact", "regex", "template"];

const MODE_LABEL: Record<Mode, string> = {
  exact: "Exact",
  regex: "Regex",
  template: "Capture",
};

interface RemappingFormProps {
  /** Pre-fill + lock the axis (Explorer: rename a specific group's axis). */
  presetAxis?: HeartbeatAxis;
  /** Pre-fill the pattern with the clicked group's raw value. */
  presetValue?: string;
  /** Pre-fill the match strategy (edit mode: seed from the rule's matchType). */
  presetMatchType?: Mode;
  /**
   * Pre-fill the target/template. For template rules pass the UI (`$N`) form —
   * callers convert the backend `\N` via `templateToDisplay`.
   */
  presetTarget?: string;
  /**
   * Edit mode: the id of the rule being edited. When set, the form saves via
   * the `edit` mutation (delete-old + create-new when identity changes; upsert
   * when only the target changes) instead of a plain create.
   */
  editRuleId?: number;
  /** Called after a successful create/edit (e.g. to close the row/dialog). */
  onDone?: () => void;
  /** Show a Cancel button (dialog/edit use). */
  onCancel?: () => void;
  /**
   * "inline" = compact single-row form (Settings card). "stacked" = vertical
   * fields with a footer (dialog).
   */
  layout?: "inline" | "stacked";
  submitLabel?: string;
}

/**
 * Single shared form for creating a rename/remapping curation rule. Used by both
 * the Settings "Name remappings" card and the Heartbeats Explorer rename dialog,
 * so there is exactly one implementation of axis + pattern + mode + target.
 * Owns the useCuration mutation (which invalidates the dashboards).
 *
 * Modes:
 *  - Exact:   literal match → target.
 *  - Regex:   pattern is a regex; matching values → target.
 *  - Capture: pattern has capture groups; target is a replacement template using
 *             `$1` (translated to backend `\1` on submit), e.g. `^@(.*)$` + `$1`
 *             strips a leading `@`.
 */
export function RemappingForm({
  presetAxis,
  presetValue,
  presetMatchType,
  presetTarget,
  editRuleId,
  onDone,
  onCancel,
  layout = "inline",
  submitLabel,
}: RemappingFormProps) {
  const { add, edit } = useCurationMutations();
  const axisLocked = presetAxis !== undefined;
  const editing = editRuleId !== undefined;

  const [axis, setAxis] = useState<HeartbeatAxis>(
    presetAxis ?? CURATABLE_AXES[0],
  );
  const [pattern, setPattern] = useState(presetValue ?? "");
  const [target, setTarget] = useState(presetTarget ?? "");
  const [mode, setMode] = useState<Mode>(presetMatchType ?? "exact");

  // Re-seed when the preset changes (e.g. the dialog opens for a new group, or
  // a different rule enters edit mode).
  useEffect(() => {
    if (presetAxis !== undefined) setAxis(presetAxis);
    setPattern(presetValue ?? "");
    setTarget(presetTarget ?? "");
    setMode(presetMatchType ?? "exact");
  }, [presetAxis, presetValue, presetTarget, presetMatchType, editRuleId]);

  const isRegexLike = mode === "regex" || mode === "template";
  const isTemplate = mode === "template";

  // Real axis values (with heartbeat counts) — power the live preview for
  // every mode (the exact-mode autocomplete in AxisValueField shares the same
  // React Query cache entry, so this is a single fetch).
  const { options } = useAxisValues(axis);

  // Client-side live preview of what the currently-selected strategy will do,
  // computed from the real axis values (no backend call), for ALL three modes:
  //  - exact:    the single matched value + its heartbeat count (if any).
  //  - regex:    the raw values matching the regex, with counts + a total.
  //  - template: raw → mapped sample rows (existing behaviour).
  const previewMatch = useMemo(() => {
    const trimmed = pattern.trim();
    if (!trimmed) return null;

    if (mode === "exact") {
      const hit = options.find((o) => o.value === trimmed);
      return {
        kind: "exact" as const,
        value: trimmed,
        count: hit?.count ?? 0,
        found: hit !== undefined,
      };
    }

    let re: RegExp;
    try {
      re = new RegExp(trimmed);
    } catch {
      return { kind: "invalid" as const };
    }
    const matched = options.filter((o) => re.test(o.value));

    if (isTemplate) {
      if (!target.trim()) return null;
      const jsTemplate = templateToJs(templateToBackend(target.trim()));
      return {
        kind: "template" as const,
        total: matched.length,
        rows: matched
          .slice(0, 5)
          .map((o) => ({ raw: o.value, mapped: o.value.replace(re, jsTemplate) })),
      };
    }

    return {
      kind: "regex" as const,
      total: matched.length,
      rows: matched.slice(0, 8).map((o) => ({ value: o.value, count: o.count ?? 0 })),
    };
  }, [mode, isTemplate, pattern, target, options]);

  function submit(e: React.FormEvent) {
    e.preventDefault();
    const matchValue = pattern.trim();
    const rawTarget = target.trim();
    if (!matchValue || !rawTarget) {
      toast.error("Enter both a pattern and a target");
      return;
    }
    if (mode === "exact" && matchValue === rawTarget) {
      // Renaming a value to itself is a no-op.
      onDone?.();
      return;
    }
    if (isRegexLike) {
      try {
        new RegExp(matchValue);
      } catch {
        toast.error("That isn't a valid regular expression");
        return;
      }
    }
    // Capture templates: accept `$N` in the UI, send backend `\N` form.
    const newValue = isTemplate ? templateToBackend(rawTarget) : rawTarget;
    const body = { axis, action: "rename" as const, matchValue, newValue, matchType: mode };

    if (editing) {
      // Rule identity is (axis, action, matchType, matchValue). If any of those
      // changed we must delete the old rule (create alone would upsert a NEW
      // key and leave the old one). If only the target changed, create upserts
      // newValue on the existing key.
      const identityChanged =
        axis !== presetAxis ||
        mode !== (presetMatchType ?? "exact") ||
        matchValue !== (presetValue ?? "");
      edit.mutate(
        { oldId: editRuleId, identityChanged, body },
        {
          onSuccess: () => {
            toast.success(`Updated remapping ${matchValue} → ${rawTarget}`);
            onDone?.();
          },
          onError: () => toast.error("Failed to update remapping"),
        },
      );
      return;
    }

    add.mutate(body, {
      onSuccess: () => {
        toast.success(`Remapped ${matchValue} → ${rawTarget}`);
        setPattern(presetValue ?? "");
        setTarget("");
        setMode("exact");
        onDone?.();
      },
      onError: () => toast.error("Failed to add remapping"),
    });
  }

  const stacked = layout === "stacked";
  const canSubmit = pattern.trim() !== "" && target.trim() !== "";
  const isPending = add.isPending || edit.isPending;

  const axisField = (
    <AxisSelect
      axes={CURATABLE_AXES}
      value={axis}
      onChange={setAxis}
      locked={axisLocked}
      triggerClassName={stacked ? "w-full" : "w-32"}
    />
  );

  const modeField = (
    <MatchTypeToggle
      modes={MODES}
      labels={MODE_LABEL}
      value={mode}
      onChange={setMode}
    />
  );

  const patternField = (
    <AxisValueField
      axis={axis}
      exact={!isRegexLike}
      value={pattern}
      onChange={setPattern}
      label={isRegexLike ? "Pattern (regex)" : "Pattern"}
      placeholder={
        isTemplate ? "^@(.*)$" : isRegexLike ? "^Meet" : "Meet - Weekly All-Hands"
      }
      className={cn(!stacked && "min-w-40 flex-1")}
    />
  );

  const targetField = (
    <div className={cn("space-y-1", !stacked && "min-w-40 flex-1")}>
      <Label className="text-xs">
        {isTemplate ? "Template ($1, $2…)" : "Target name"}
      </Label>
      <Input
        value={target}
        onChange={(e) => setTarget(e.target.value)}
        placeholder={isTemplate ? "$1" : "Meeting"}
        className="h-8 font-mono"
      />
    </div>
  );

  const hint = isRegexLike && (
    <p className="text-xs text-muted-foreground">
      {isTemplate ? (
        <>
          The pattern is a regex with capture groups; the template references
          them with <span className="font-mono">$1</span>,{" "}
          <span className="font-mono">$2</span>… (e.g.{" "}
          <span className="font-mono">^@(.*)$</span> →{" "}
          <span className="font-mono">$1</span> strips a leading{" "}
          <span className="font-mono">@</span>).
        </>
      ) : (
        <>
          The pattern is a regex matched against raw{" "}
          {axisLabel(axis).toLowerCase()} values (e.g.{" "}
          <span className="font-mono">^Meet</span> or{" "}
          <span className="font-mono">Meet - .*</span>).
        </>
      )}
    </p>
  );

  const preview =
    previewMatch &&
    (previewMatch.kind === "invalid" ? (
      <MatchPreviewContainer>
        <p className="text-xs text-muted-foreground">
          Enter a valid regular expression to preview matches.
        </p>
      </MatchPreviewContainer>
    ) : previewMatch.kind === "exact" ? (
      <MatchPreviewContainer>
        {previewMatch.found ? (
          <p className="text-xs text-muted-foreground">
            Matches{" "}
            <span className="font-mono font-medium text-foreground">
              {previewMatch.value}
            </span>{" "}
            · {previewMatch.count.toLocaleString()} heartbeat
            {previewMatch.count === 1 ? "" : "s"}
          </p>
        ) : (
          <p className="text-xs text-muted-foreground">
            No heartbeats match this value yet.
          </p>
        )}
      </MatchPreviewContainer>
    ) : previewMatch.kind === "regex" ? (
      <MatchPreviewList
        title={
          <>
            Matches {previewMatch.total.toLocaleString()} value
            {previewMatch.total === 1 ? "" : "s"}
          </>
        }
        rows={previewMatch.rows}
        emptyText="No values match yet."
      />
    ) : (
      <MatchPreviewList
        title={
          <>
            Preview · matches {previewMatch.total.toLocaleString()} value
            {previewMatch.total === 1 ? "" : "s"}
          </>
        }
        rows={previewMatch.rows}
        emptyText={null}
        rowKey={(row) => row.raw}
        renderRow={(row) => (
          <div className="flex items-center gap-1.5 text-xs">
            <span className="truncate font-mono" title={row.raw}>
              {row.raw}
            </span>
            <ArrowRight className="h-3 w-3 shrink-0 text-muted-foreground" />
            <span
              className="truncate font-mono font-medium"
              title={row.mapped}
            >
              {row.mapped}
            </span>
          </div>
        )}
      />
    ));

  if (stacked) {
    return (
      <form onSubmit={submit} className="space-y-4">
        {!axisLocked && axisField}
        {modeField}
        {patternField}
        <div className="flex items-center justify-center text-muted-foreground">
          <ArrowRight className="h-4 w-4" />
        </div>
        {targetField}
        {hint}
        {preview}
        <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          {onCancel && (
            <Button type="button" variant="secondary" onClick={onCancel}>
              Cancel
            </Button>
          )}
          <Button type="submit" disabled={isPending || !canSubmit}>
            {isPending ? "Saving..." : (submitLabel ?? "Save remapping")}
          </Button>
        </div>
      </form>
    );
  }

  // Inline (Settings) layout.
  return (
    <form onSubmit={submit} className="space-y-3 rounded-md border bg-muted/30 p-3">
      <div className="flex flex-wrap items-end gap-2">
        {axisField}
        {modeField}
        {patternField}
        <ArrowRight className="mb-2 hidden h-4 w-4 shrink-0 text-muted-foreground sm:block" />
        {targetField}
        <Button
          type="submit"
          size="sm"
          className="h-8"
          disabled={isPending || !canSubmit}
        >
          {editing ? <Check className="h-4 w-4" /> : <Plus className="h-4 w-4" />}
          {submitLabel ?? (editing ? "Save" : "Add")}
        </Button>
        {onCancel && (
          <Button
            type="button"
            size="sm"
            variant="secondary"
            className="h-8"
            onClick={onCancel}
          >
            Cancel
          </Button>
        )}
      </div>
      {hint}
      {preview}
    </form>
  );
}
