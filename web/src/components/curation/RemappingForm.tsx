import { useEffect, useState } from "react";
import { ArrowRight, Plus } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { axisLabel } from "@/components/heartbeats/axes";
import { useCurationMutations } from "@/hooks/useCuration";
import { cn } from "@/lib/utils";
import type { HeartbeatAxis } from "@/types/api";

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
 * so there is exactly one implementation of axis + pattern + regex toggle +
 * target. Owns the useCuration mutation (which invalidates the dashboards).
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
  const [regex, setRegex] = useState(false);

  // Re-seed when the preset changes (e.g. the dialog opens for a new group).
  useEffect(() => {
    if (presetAxis !== undefined) setAxis(presetAxis);
    setPattern(presetValue ?? "");
    setTarget("");
    setRegex(false);
  }, [presetAxis, presetValue]);

  function submit(e: React.FormEvent) {
    e.preventDefault();
    const matchValue = pattern.trim();
    const newValue = target.trim();
    if (!matchValue || !newValue) {
      toast.error("Enter both a pattern and a target name");
      return;
    }
    if (matchValue === newValue && !regex) {
      // Renaming a value to itself is a no-op.
      onDone?.();
      return;
    }
    if (regex) {
      try {
        new RegExp(matchValue);
      } catch {
        toast.error("That isn't a valid regular expression");
        return;
      }
    }
    add.mutate(
      {
        axis,
        action: "rename",
        matchValue,
        newValue,
        matchType: regex ? "regex" : "exact",
      },
      {
        onSuccess: () => {
          toast.success(`Remapped ${matchValue} → ${newValue}`);
          setPattern(presetValue ?? "");
          setTarget("");
          setRegex(false);
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

  const patternField = (
    <div className={cn("space-y-1", !stacked && "min-w-40 flex-1")}>
      <div className="flex items-center justify-between">
        <Label className="text-xs">{regex ? "Pattern (regex)" : "Pattern"}</Label>
        <label className="flex cursor-pointer items-center gap-1.5 text-xs text-muted-foreground">
          <input
            type="checkbox"
            checked={regex}
            onChange={(e) => setRegex(e.target.checked)}
            className="h-3.5 w-3.5 accent-primary"
          />
          regex
        </label>
      </div>
      <Input
        value={pattern}
        onChange={(e) => setPattern(e.target.value)}
        placeholder={regex ? "^Meet" : "Meet - Weekly All-Hands"}
        className="h-8 font-mono"
      />
    </div>
  );

  const targetField = (
    <div className={cn("space-y-1", !stacked && "min-w-40 flex-1")}>
      <Label className="text-xs">Target name</Label>
      <Input
        value={target}
        onChange={(e) => setTarget(e.target.value)}
        placeholder="Meeting"
        className="h-8 font-mono"
      />
    </div>
  );

  const regexHint = regex && (
    <p className="text-xs text-muted-foreground">
      The pattern is a regular expression matched against raw{" "}
      {axisLabel(axis).toLowerCase()} values (e.g.{" "}
      <span className="font-mono">^Meet</span> or{" "}
      <span className="font-mono">Meet - .*</span>).
    </p>
  );

  if (stacked) {
    return (
      <form onSubmit={submit} className="space-y-4">
        {!axisLocked && axisField}
        {patternField}
        <div className="flex items-center justify-center text-muted-foreground">
          <ArrowRight className="h-4 w-4" />
        </div>
        {targetField}
        {regexHint}
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
      {regexHint}
    </form>
  );
}
