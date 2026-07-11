import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Plus } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { AxisSelect } from "@/features/rules/AxisSelect";
import { AxisValueField } from "@/features/rules/AxisValueField";
import { MatchTypeToggle } from "@/features/rules/MatchTypeToggle";
import { MatchPreviewList } from "@/features/rules/MatchPreviewList";
import { CURATABLE_AXES } from "@/features/rules/axes";
import { axisLabel } from "@/lib/axes";
import { useSpaceMutations } from "@/features/spaces/useSpaces";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import type { HeartbeatAxis, SpaceMatchType } from "@/types/api";

const MODES: readonly SpaceMatchType[] = ["exact", "regex"];

const MODE_LABEL: Record<SpaceMatchType, string> = {
  exact: "Exact",
  regex: "Regex",
};

interface SpaceRuleFormProps {
  /** The Space to add the rule to. */
  spaceId: number | string;
  /** Called after a successful add (e.g. to reset a section). */
  onDone?: () => void;
}

/**
 * Form for adding a membership rule to a Space. Built from the same shared
 * rule-form subcomponents as RemappingForm (axis dropdown + exact/regex
 * segmented toggle + value field), plus a LIVE "matches N values" preview
 * (debounced) via getSpacePreview. Owns the useSpaceMutations().addRule
 * mutation (which invalidates the scoped dashboards).
 */
export function SpaceRuleForm({ spaceId, onDone }: SpaceRuleFormProps) {
  const { addRule } = useSpaceMutations();

  const [axis, setAxis] = useState<HeartbeatAxis>(CURATABLE_AXES[0]);
  const [matchValue, setMatchValue] = useState("");
  const [mode, setMode] = useState<SpaceMatchType>("exact");

  // Debounce the value so the live preview doesn't fire on every keystroke.
  const [debounced, setDebounced] = useState("");
  useEffect(() => {
    const t = setTimeout(() => setDebounced(matchValue.trim()), 300);
    return () => clearTimeout(t);
  }, [matchValue]);

  // Regex validity gates the preview query (an invalid regex would 400).
  const regexValid = useMemo(() => {
    if (mode !== "regex" || !debounced) return true;
    try {
      new RegExp(debounced);
      return true;
    } catch {
      return false;
    }
  }, [mode, debounced]);

  const previewQuery = useQuery({
    queryKey: qk.spacePreview(axis, mode, debounced),
    enabled: debounced !== "" && regexValid,
    queryFn: () =>
      api.getSpacePreview({ axis, matchValue: debounced, matchType: mode }),
  });

  function submit(e: React.FormEvent) {
    e.preventDefault();
    const value = matchValue.trim();
    if (!value) {
      toast.error("Enter a value to match");
      return;
    }
    if (mode === "regex") {
      try {
        new RegExp(value);
      } catch {
        toast.error("That isn't a valid regular expression");
        return;
      }
    }
    addRule.mutate(
      { id: spaceId, body: { axis, matchValue: value, matchType: mode } },
      {
        onSuccess: () => {
          toast.success(`Added rule ${axisLabel(axis)} · ${value}`);
          setMatchValue("");
          setDebounced("");
          setMode("exact");
          onDone?.();
        },
        onError: () => toast.error("Failed to add rule"),
      },
    );
  }

  const canSubmit = matchValue.trim() !== "";
  const preview = previewQuery.data;

  return (
    <form
      onSubmit={submit}
      className="space-y-3 rounded-md border bg-muted/30 p-3"
    >
      <div className="flex flex-wrap items-end gap-2">
        <AxisSelect
          axes={CURATABLE_AXES}
          value={axis}
          onChange={setAxis}
          triggerClassName="w-32"
        />

        <MatchTypeToggle
          modes={MODES}
          labels={MODE_LABEL}
          value={mode}
          onChange={setMode}
        />

        <AxisValueField
          axis={axis}
          exact={mode === "exact"}
          value={matchValue}
          onChange={setMatchValue}
          label={mode === "regex" ? "Value (regex)" : "Value"}
          placeholder={
            mode === "regex"
              ? "^catalyst"
              : `A ${axisLabel(axis).toLowerCase()}…`
          }
          className="min-w-40 flex-1"
        />

        <Button
          type="submit"
          size="sm"
          className="h-8"
          disabled={addRule.isPending || !canSubmit}
        >
          <Plus className="h-4 w-4" />
          Add rule
        </Button>
      </div>

      {mode === "regex" && (
        <p className="text-xs text-muted-foreground">
          The value is a regex matched against raw{" "}
          {axisLabel(axis).toLowerCase()} values (e.g.{" "}
          <span className="font-mono">^catalyst</span> or{" "}
          <span className="font-mono">.*-api$</span>).
        </p>
      )}

      {debounced !== "" && regexValid && (
        <MatchPreviewList
          title={
            previewQuery.isLoading
              ? "Matching…"
              : `Matches ${preview?.values.length ?? 0} value${
                  (preview?.values.length ?? 0) === 1 ? "" : "s"
                }${preview?.truncated ? "+" : ""}`
          }
          rows={preview?.values.slice(0, 8) ?? []}
          emptyText={
            preview && !previewQuery.isLoading ? "No values match yet." : null
          }
        />
      )}
    </form>
  );
}
