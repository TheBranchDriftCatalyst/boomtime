import { useEffect, useMemo, useState } from "react";
import { ArrowRight, Plus } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Combobox } from "@/components/ui/combobox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { axisLabel } from "@/components/heartbeats/axes";
import { useAxisValues } from "@/hooks/useAxisValues";
import { useCurationMutations } from "@/hooks/useCuration";
import { templateToBackend, templateToJs } from "@/lib/remapDisplay";
import { cn } from "@/lib/utils";
import type { CurationMatchType, HeartbeatAxis } from "@/types/api";

// Axes that support name remappings (rename rules) — matches the renamable axes
// in the Heartbeats explorer (excludes synthetic `day` and file-path `entity`).
const REMAP_AXES: readonly HeartbeatAxis[] = [
  "project",
  "language",
  "editor",
  "plugin",
  "machine",
  "platform",
  "branch",
  "category",
];

type Mode = CurationMatchType; // "exact" | "regex" | "template"

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
  /** Called after a successful create (e.g. to close a dialog). */
  onDone?: () => void;
  /** Show a Cancel button (dialog use). */
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
  onDone,
  onCancel,
  layout = "inline",
  submitLabel,
}: RemappingFormProps) {
  const { add } = useCurationMutations();
  const axisLocked = presetAxis !== undefined;

  const [axis, setAxis] = useState<HeartbeatAxis>(presetAxis ?? REMAP_AXES[0]);
  const [pattern, setPattern] = useState(presetValue ?? "");
  const [target, setTarget] = useState("");
  const [mode, setMode] = useState<Mode>("exact");

  // Re-seed when the preset changes (e.g. the dialog opens for a new group).
  useEffect(() => {
    if (presetAxis !== undefined) setAxis(presetAxis);
    setPattern(presetValue ?? "");
    setTarget("");
    setMode("exact");
  }, [presetAxis, presetValue]);

  const isRegexLike = mode === "regex" || mode === "template";
  const isTemplate = mode === "template";

  // Real axis values (with heartbeat counts) — power the exact-mode
  // autocomplete AND the live preview for every mode. Always fetched so the
  // exact combobox and previews can show matching values as the user types.
  const { options, isLoading: axisLoading } = useAxisValues(axis);

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

    add.mutate(
      { axis, action: "rename", matchValue, newValue, matchType: mode },
      {
        onSuccess: () => {
          toast.success(`Remapped ${matchValue} → ${rawTarget}`);
          setPattern(presetValue ?? "");
          setTarget("");
          setMode("exact");
          onDone?.();
        },
        onError: () => toast.error("Failed to add remapping"),
      },
    );
  }

  const stacked = layout === "stacked";
  const canSubmit = pattern.trim() !== "" && target.trim() !== "";

  const axisField = (
    <div className="space-y-1">
      <Label className="text-xs">Axis</Label>
      {axisLocked ? (
        <div className="flex h-8 w-32 items-center rounded-md border bg-muted px-3 text-sm text-muted-foreground">
          {axisLabel(axis)}
        </div>
      ) : (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              variant="outline"
              size="sm"
              className={cn("justify-between", stacked ? "w-full" : "w-32")}
            >
              {axisLabel(axis)}
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="max-h-72 overflow-y-auto">
            {REMAP_AXES.map((a) => (
              <DropdownMenuItem key={a} onSelect={() => setAxis(a)}>
                {axisLabel(a)}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  );

  const modeField = (
    <div className="space-y-1">
      <Label className="text-xs">Match</Label>
      <div className="inline-flex h-8 items-center rounded-md border p-0.5">
        {(["exact", "regex", "template"] as Mode[]).map((m) => (
          <button
            key={m}
            type="button"
            aria-pressed={mode === m}
            onClick={() => setMode(m)}
            className={cn(
              "h-full rounded px-2 text-xs font-medium transition-colors",
              mode === m
                ? "bg-primary text-primary-foreground"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            {MODE_LABEL[m]}
          </button>
        ))}
      </div>
    </div>
  );

  const patternField = (
    <div className={cn("space-y-1", !stacked && "min-w-40 flex-1")}>
      <Label className="text-xs">
        {isRegexLike ? "Pattern (regex)" : "Pattern"}
      </Label>
      {isRegexLike ? (
        <Input
          value={pattern}
          onChange={(e) => setPattern(e.target.value)}
          placeholder={isTemplate ? "^@(.*)$" : "^Meet"}
          className="h-8 font-mono"
        />
      ) : (
        <Combobox
          options={options}
          value={pattern || null}
          onSelect={setPattern}
          loading={axisLoading}
          creatable
          placeholder="Meet - Weekly All-Hands"
          searchPlaceholder={`Search ${axisLabel(axis).toLowerCase()}s…`}
          emptyText={`No ${axisLabel(axis).toLowerCase()} values found.`}
          className="h-8 font-mono"
        />
      )}
    </div>
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

  const preview = previewMatch && (
    <div className="space-y-1 rounded-md border bg-background/60 p-2">
      {previewMatch.kind === "invalid" ? (
        <p className="text-xs text-muted-foreground">
          Enter a valid regular expression to preview matches.
        </p>
      ) : previewMatch.kind === "exact" ? (
        previewMatch.found ? (
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
        )
      ) : previewMatch.kind === "regex" ? (
        <>
          <p className="text-xs font-medium text-muted-foreground">
            Matches {previewMatch.total.toLocaleString()} value
            {previewMatch.total === 1 ? "" : "s"}
          </p>
          {previewMatch.rows.map((row) => (
            <div
              key={row.value}
              className="flex items-center justify-between gap-2 text-xs"
            >
              <span className="truncate font-mono" title={row.value}>
                {row.value}
              </span>
              <span className="shrink-0 tabular-nums text-muted-foreground">
                {row.count.toLocaleString()}
              </span>
            </div>
          ))}
          {previewMatch.total === 0 && (
            <p className="text-xs text-muted-foreground">No values match yet.</p>
          )}
        </>
      ) : (
        <>
          <p className="text-xs font-medium text-muted-foreground">
            Preview · matches {previewMatch.total.toLocaleString()} value
            {previewMatch.total === 1 ? "" : "s"}
          </p>
          {previewMatch.rows.map((row) => (
            <div key={row.raw} className="flex items-center gap-1.5 text-xs">
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
          ))}
        </>
      )}
    </div>
  );

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
          <Button type="submit" disabled={add.isPending || !canSubmit}>
            {add.isPending ? "Saving..." : (submitLabel ?? "Save remapping")}
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
          disabled={add.isPending || !canSubmit}
        >
          <Plus className="h-4 w-4" />
          {submitLabel ?? "Add"}
        </Button>
      </div>
      {hint}
      {preview}
    </form>
  );
}
