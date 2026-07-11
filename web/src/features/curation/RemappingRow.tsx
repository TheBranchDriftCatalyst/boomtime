import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowRight,
  ChevronDown,
  ChevronRight,
  Loader2,
  Pencil,
  X,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { RemappingForm } from "@/features/curation/RemappingForm";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { templateToDisplay } from "@/features/curation/remapDisplay";
import type {
  CurationMatchType,
  CurationRule,
  HeartbeatAxis,
} from "@/types/api";

export function RemappingRow({
  rule,
  onRemove,
}: {
  rule: CurationRule;
  onRemove: (rule: CurationRule) => void;
}) {
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const matchType: CurationMatchType = rule.matchType ?? "exact";
  // Badge for non-exact rules ("regex" / "template" capture rules).
  const modeBadge =
    rule.matchType === "regex"
      ? "regex"
      : rule.matchType === "template"
        ? "capture"
        : null;

  // All hooks must run before any conditional return (Rules of Hooks) — the
  // edit-mode early return lives below these, not above them.
  const affected = useQuery({
    queryKey: qk.curationAffected(rule.id),
    queryFn: () => api.getCurationRuleAffected(rule.id),
    enabled: open,
    staleTime: 30_000,
  });

  const total = useMemo(
    () => (affected.data?.values ?? []).reduce((s, v) => s + v.count, 0),
    [affected.data],
  );

  if (editing) {
    // Inline-expand the row into a pre-filled edit form. Template targets are
    // stored with backend `\N` backrefs; convert to the authoring `$N` form.
    const presetTarget =
      matchType === "template"
        ? templateToDisplay(rule.newValue ?? "")
        : (rule.newValue ?? "");
    return (
      <RemappingForm
        layout="inline"
        editRuleId={rule.id}
        presetAxis={rule.axis as HeartbeatAxis}
        presetValue={rule.matchValue}
        presetMatchType={matchType}
        presetTarget={presetTarget}
        submitLabel="Save"
        onDone={() => setEditing(false)}
        onCancel={() => setEditing(false)}
      />
    );
  }

  return (
    <div className="rounded-md border bg-secondary/40 text-sm">
      <div className="flex items-center gap-2 px-2.5 py-1.5">
        <button
          className="flex flex-1 items-center gap-2 text-left"
          onClick={() => setOpen((o) => !o)}
          title="View the raw values this rule matches"
        >
          <span className="flex h-4 w-4 items-center justify-center text-muted-foreground">
            {open ? (
              <ChevronDown className="h-4 w-4" />
            ) : (
              <ChevronRight className="h-4 w-4" />
            )}
          </span>
          <span className="font-mono">{rule.matchValue}</span>
          {modeBadge && (
            <Badge
              variant="outline"
              className="shrink-0 border-violet-500/40 text-[10px] uppercase text-violet-400"
            >
              {modeBadge}
            </Badge>
          )}
          <ArrowRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          <span className="font-mono font-medium">{rule.newValue}</span>
        </button>
        <button
          onClick={() => setEditing(true)}
          title="Edit this remapping"
          className="rounded-full p-0.5 text-muted-foreground hover:bg-background hover:text-foreground"
        >
          <Pencil className="h-3.5 w-3.5" />
        </button>
        <button
          onClick={() => onRemove(rule)}
          title="Remove remapping (reverts the merge)"
          className="rounded-full p-0.5 text-muted-foreground hover:bg-background hover:text-foreground"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      </div>

      {open && (
        <div className="border-t px-3 py-2">
          {affected.isLoading ? (
            <p className="flex items-center gap-2 py-2 text-xs text-muted-foreground">
              <Loader2 className="h-3.5 w-3.5 animate-spin" /> Loading matched
              values…
            </p>
          ) : affected.isError ? (
            <p className="py-2 text-xs text-destructive">
              Failed to load matched values.
            </p>
          ) : (affected.data?.values.length ?? 0) === 0 ? (
            <p className="py-2 text-xs text-muted-foreground">
              No current values match this pattern.
            </p>
          ) : (
            <>
              <p className="mb-1.5 text-xs text-muted-foreground">
                Matches {affected.data!.values.length.toLocaleString()} value
                {affected.data!.values.length === 1 ? "" : "s"} ·{" "}
                {total.toLocaleString()} heartbeats
                {affected.data!.truncated ? " (showing top matches)" : ""}
              </p>
              <div className="max-h-56 space-y-1 overflow-y-auto">
                {affected.data!.values.map((v) => (
                  <div
                    key={v.value}
                    className="flex items-center gap-1.5 rounded px-1.5 py-0.5"
                  >
                    <span className="truncate font-mono text-xs" title={v.value}>
                      {v.value}
                    </span>
                    {v.mappedTo != null && v.mappedTo !== v.value && (
                      <>
                        <ArrowRight className="h-3 w-3 shrink-0 text-muted-foreground" />
                        <span
                          className="truncate font-mono text-xs font-medium text-violet-400"
                          title={v.mappedTo}
                        >
                          {v.mappedTo}
                        </span>
                      </>
                    )}
                    <span className="ml-auto shrink-0 font-mono text-xs text-muted-foreground">
                      {v.count.toLocaleString()}
                    </span>
                  </div>
                ))}
              </div>
            </>
          )}
        </div>
      )}
    </div>
  );
}
