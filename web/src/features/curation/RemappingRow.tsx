import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowRight,
  ChevronDown,
  ChevronRight,
  Eye,
  EyeOff,
  Loader2,
  Pencil,
  Trash2,
  X,
  Zap,
} from "lucide-react";
import { Badge } from "@thebranchdriftcatalyst/catalyst-ui/ui/badge";
import { RemappingForm } from "@/features/curation/RemappingForm";
import { useCurationMutations } from "@/features/curation/useCuration";
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
  onApply,
  onPurge,
}: {
  rule: CurationRule;
  onRemove: (rule: CurationRule) => void;
  // gaka-cr4: click handler for the destructive "apply mapping" button
  // (rename rules only — Zap icon). Kept as a prop (not a hook here) so the
  // parent owns the modal state and a single modal instance is reused
  // across rows — plays nicely with the parent's query-invalidation-on-
  // success pattern.
  onApply?: (rule: CurationRule) => void;
  // gaka-due: click handler for the destructive "purge" button (hide rules
  // only — Trash2 icon). Same pattern as onApply; parent owns the modal.
  onPurge?: (rule: CurationRule) => void;
}) {
  // Icon visibility dispatches on rule.action so a hide row never shows the
  // rename-only Zap and vice versa. Both destructive buttons still require
  // the parent to pass a handler — the parent decides whether the feature
  // is wired in this view at all.
  const isRename = rule.action === "rename";
  const isHide = rule.action === "hide";
  // gaka-dfd: an older backend may omit `enabled`; treat missing as `true`
  // (the pre-feature default) so pre-migration installs still render sane.
  const isEnabled = rule.enabled !== false;
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState(false);
  const { toggle } = useCurationMutations();
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
    <div
      className={
        "rounded-md border bg-secondary/40 text-sm transition-opacity " +
        // gaka-dfd: dim the whole row when the rule is paused so the "this
        // isn't doing anything right now" signal is present peripherally.
        // Opacity beats a badge here — badges compete for attention with
        // the existing HIDDEN / capture tags.
        (isEnabled ? "" : "opacity-60")
      }
    >
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
          {isHide && (
            // Hide rules have no target — surface an EyeOff badge so the row
            // is visually distinct from a rename (which shows source → target).
            <Badge
              variant="outline"
              className="shrink-0 border-amber-500/40 text-[10px] uppercase text-amber-400"
            >
              <EyeOff className="mr-1 inline h-3 w-3" />
              hidden
            </Badge>
          )}
          {isRename && (
            <>
              <ArrowRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              <span className="font-mono font-medium">{rule.newValue}</span>
            </>
          )}
        </button>
        {/*
          gaka-dfd: pause / resume toggle. First icon in the action group so
          the "is this rule live?" affordance is the leftmost decision.
          EyeOff = "currently active, click to pause" (an eye that stops
          watching); Eye = "currently paused, click to resume watching".
          Pass the EXACT desired state (not a flip) so double-clicks can't
          land on the wrong value.
        */}
        <button
          onClick={() =>
            toggle.mutate({ id: rule.id, enabled: !isEnabled })
          }
          disabled={toggle.isPending}
          title={
            isEnabled
              ? "Pause rule (keeps definition, stops applying)"
              : "Enable rule (start applying again)"
          }
          aria-label={
            isEnabled ? "Pause curation rule" : "Enable curation rule"
          }
          aria-pressed={!isEnabled}
          className="rounded-full p-0.5 text-muted-foreground hover:bg-background hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50"
        >
          {isEnabled ? (
            <EyeOff className="h-3.5 w-3.5" />
          ) : (
            <Eye className="h-3.5 w-3.5" />
          )}
        </button>
        <button
          onClick={() => setEditing(true)}
          title="Edit this rule"
          className="rounded-full p-0.5 text-muted-foreground hover:bg-background hover:text-foreground"
        >
          <Pencil className="h-3.5 w-3.5" />
        </button>
        {isRename && onApply && (
          <button
            onClick={() => onApply(rule)}
            title="Apply mapping destructively (rewrite raw rows, remove mapping)"
            aria-label="Apply mapping destructively"
            className="rounded-full p-0.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
          >
            <Zap className="h-3.5 w-3.5" />
          </button>
        )}
        {isHide && onPurge && (
          <button
            onClick={() => onPurge(rule)}
            title="Purge hidden rows destructively (DELETE raw heartbeats, remove rule)"
            aria-label="Purge hidden rows destructively"
            // Slightly redder hover tint than Zap — Trash2 obliterates data,
            // Zap only rewrites it. Communicate the extra danger.
            className="rounded-full p-0.5 text-muted-foreground hover:bg-destructive/20 hover:text-destructive"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        )}
        <button
          onClick={() => onRemove(rule)}
          title={
            isHide
              ? "Remove hide rule (rows reappear on dashboards)"
              : "Remove remapping (reverts the merge)"
          }
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
