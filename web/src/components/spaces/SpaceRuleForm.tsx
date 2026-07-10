import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Plus } from "lucide-react";
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
import { useSpaceMutations } from "@/hooks/useSpaces";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import type { HeartbeatAxis, SpaceMatchType } from "@/types/api";

// Axes a Space membership rule can target. Mirrors the suppressible/curatable
// axes (excludes synthetic `day`/`isWrite` and file-path `entity`/`userAgent`).
const RULE_AXES: readonly HeartbeatAxis[] = [
  "project",
  "language",
  "editor",
  "plugin",
  "machine",
  "platform",
  "branch",
  "category",
];

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
 * Form for adding a membership rule to a Space. Modeled on RemappingForm:
 * axis dropdown + exact/regex segmented toggle + value input, plus a LIVE
 * "matches N values" preview (debounced) via getSpacePreview. Owns the
 * useSpaceMutations().addRule mutation (which invalidates the scoped dashboards).
 */
export function SpaceRuleForm({ spaceId, onDone }: SpaceRuleFormProps) {
  const { addRule } = useSpaceMutations();

  const [axis, setAxis] = useState<HeartbeatAxis>(RULE_AXES[0]);
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
    queryKey: ["space-preview", axis, mode, debounced],
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
        <div className="space-y-1">
          <Label className="text-xs">Axis</Label>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="w-32 justify-between"
              >
                {axisLabel(axis)}
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent
              align="start"
              className="max-h-72 overflow-y-auto"
            >
              {RULE_AXES.map((a) => (
                <DropdownMenuItem key={a} onSelect={() => setAxis(a)}>
                  {axisLabel(a)}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
        </div>

        <div className="space-y-1">
          <Label className="text-xs">Match</Label>
          <div className="inline-flex h-8 items-center rounded-md border p-0.5">
            {(["exact", "regex"] as SpaceMatchType[]).map((m) => (
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

        <div className="min-w-40 flex-1 space-y-1">
          <Label className="text-xs">
            {mode === "regex" ? "Value (regex)" : "Value"}
          </Label>
          <Input
            value={matchValue}
            onChange={(e) => setMatchValue(e.target.value)}
            placeholder={
              mode === "regex" ? "^catalyst" : `A ${axisLabel(axis).toLowerCase()}…`
            }
            className="h-8 font-mono"
          />
        </div>

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
        <div className="space-y-1 rounded-md border bg-background/60 p-2">
          <p className="text-xs font-medium text-muted-foreground">
            {previewQuery.isLoading
              ? "Matching…"
              : `Matches ${preview?.values.length ?? 0} value${
                  (preview?.values.length ?? 0) === 1 ? "" : "s"
                }${preview?.truncated ? "+" : ""}`}
          </p>
          {preview?.values.slice(0, 8).map((v) => (
            <div
              key={v.value}
              className="flex items-center justify-between gap-2 text-xs"
            >
              <span className="truncate font-mono" title={v.value}>
                {v.value}
              </span>
              <span className="shrink-0 tabular-nums text-muted-foreground">
                {v.count}
              </span>
            </div>
          ))}
          {preview && preview.values.length === 0 && !previewQuery.isLoading && (
            <p className="text-xs text-muted-foreground">
              No values match yet.
            </p>
          )}
        </div>
      )}
    </form>
  );
}
