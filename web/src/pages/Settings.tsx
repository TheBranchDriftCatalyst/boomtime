import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import {
  ArrowRight,
  ChevronDown,
  ChevronRight,
  Loader2,
  X,
} from "lucide-react";
import { toast } from "sonner";
import { PageToolbar } from "@/components/toolbar/PageToolbar";
import { Spinner } from "@/components/Spinner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Combobox } from "@/components/ui/combobox";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { RemappingForm } from "@/components/curation/RemappingForm";
import { axisLabel } from "@/components/heartbeats/axes";
import { useAxisValues } from "@/hooks/useAxisValues";
import { useCurationMutations, useCurationRules } from "@/hooks/useCuration";
import { api } from "@/lib/api";
import type { CurationRule, HeartbeatAxis } from "@/types/api";

// Axes exposed in the "hidden sources" picker.
const SOURCE_AXES = [
  { axis: "editor", label: "Editor" },
  { axis: "plugin", label: "Plugin" },
  { axis: "machine", label: "Machine" },
] as const;

const AXIS_LABEL: Record<string, string> = {
  project: "Project",
  editor: "Editor",
  plugin: "Plugin",
  machine: "Machine",
};

export function Settings() {
  const { data, isLoading } = useCurationRules();
  const { add, remove } = useCurationMutations();

  const hides = useMemo(
    () => (data?.rules ?? []).filter((r) => r.action === "hide"),
    [data],
  );
  const hiddenProjects = hides.filter((r) => r.axis === "project");
  const hiddenSources = hides.filter((r) => r.axis !== "project");

  const renames = useMemo(
    () => (data?.rules ?? []).filter((r) => r.action === "rename"),
    [data],
  );

  function unhide(rule: CurationRule) {
    remove.mutate(rule.id, {
      onSuccess: () => toast.success(`Unhid ${rule.matchValue}`),
      onError: () => toast.error("Failed to unhide"),
    });
  }

  function removeRename(rule: CurationRule) {
    remove.mutate(rule.id, {
      onSuccess: () =>
        toast.success(`Removed remapping ${rule.matchValue} → ${rule.newValue}`),
      onError: () => toast.error("Failed to remove remapping"),
    });
  }

  function addHide(axis: string, value: string) {
    const matchValue = value.trim();
    if (!matchValue) return;
    if (
      hides.some((r) => r.axis === axis && r.matchValue === matchValue)
    ) {
      toast.info("Already hidden");
      return;
    }
    add.mutate(
      { axis, action: "hide", matchValue },
      {
        onSuccess: () => toast.success(`Hid ${matchValue}`),
        onError: () => toast.error("Failed to hide"),
      },
    );
  }

  return (
    <div>
      <PageToolbar title="Settings" />

      <div className="max-w-3xl space-y-6">
        <div>
          <h2 className="text-lg font-semibold">Data curation</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            Hidden items are excluded from your dashboards but never deleted;
            unhide anytime. To rename or merge values, use the{" "}
            <Link
              to="/app/heartbeats"
              className="font-medium text-primary hover:underline"
            >
              Heartbeats
            </Link>{" "}
            explorer.
          </p>
        </div>

        {isLoading ? (
          <Spinner />
        ) : (
          <>
            <HiddenProjectsCard
              rules={hiddenProjects}
              onAdd={(v) => addHide("project", v)}
              onRemove={unhide}
            />
            <HiddenSourcesCard
              rules={hiddenSources}
              onAdd={addHide}
              onRemove={unhide}
            />
            <NameRemappingsCard rules={renames} onRemove={removeRename} />
          </>
        )}
      </div>
    </div>
  );
}

function HiddenProjectsCard({
  rules,
  onAdd,
  onRemove,
}: {
  rules: CurationRule[];
  onAdd: (value: string) => void;
  onRemove: (rule: CurationRule) => void;
}) {
  const { options, isLoading } = useAxisValues("project");

  // Don't offer already-hidden projects.
  const hiddenSet = useMemo(
    () => new Set(rules.map((r) => r.matchValue)),
    [rules],
  );
  const available = useMemo(
    () => options.filter((o) => !hiddenSet.has(o.value)),
    [options, hiddenSet],
  );

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Hidden projects</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <Combobox
          options={available}
          value={null}
          onSelect={onAdd}
          loading={isLoading}
          placeholder="Select a project to hide..."
          searchPlaceholder="Search projects..."
          emptyText="No projects found."
        />

        {rules.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            No hidden projects.
          </p>
        ) : (
          <div className="flex flex-wrap gap-2">
            {rules.map((r) => (
              <Badge key={r.id} variant="secondary" className="gap-1 py-1 pl-2.5">
                {r.matchValue}
                <button
                  onClick={() => onRemove(r)}
                  title="Unhide"
                  className="rounded-full p-0.5 hover:bg-background"
                >
                  <X className="h-3 w-3" />
                </button>
              </Badge>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function HiddenSourcesCard({
  rules,
  onAdd,
  onRemove,
}: {
  rules: CurationRule[];
  onAdd: (axis: string, value: string) => void;
  onRemove: (rule: CurationRule) => void;
}) {
  const [axis, setAxis] = useState<string>(SOURCE_AXES[0].axis);
  const { options, isLoading } = useAxisValues(axis as HeartbeatAxis);

  const grouped = useMemo(() => {
    const map = new Map<string, CurationRule[]>();
    for (const r of rules) {
      const arr = map.get(r.axis) ?? [];
      arr.push(r);
      map.set(r.axis, arr);
    }
    return map;
  }, [rules]);

  const axisLabel = SOURCE_AXES.find((a) => a.axis === axis)?.label ?? axis;

  // Exclude values already hidden for the selected axis.
  const hiddenForAxis = useMemo(
    () =>
      new Set(
        rules.filter((r) => r.axis === axis).map((r) => r.matchValue),
      ),
    [rules, axis],
  );
  const available = useMemo(
    () => options.filter((o) => !hiddenForAxis.has(o.value)),
    [options, hiddenForAxis],
  );

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Hidden sources</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex gap-2">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                type="button"
                variant="outline"
                className="w-32 shrink-0 justify-between"
              >
                {axisLabel}
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start">
              {SOURCE_AXES.map((a) => (
                <DropdownMenuItem key={a.axis} onSelect={() => setAxis(a.axis)}>
                  {a.label}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
          <Combobox
            options={available}
            value={null}
            onSelect={(v) => onAdd(axis, v)}
            loading={isLoading}
            placeholder={`Select a ${axisLabel.toLowerCase()} to hide...`}
            searchPlaceholder={`Search ${axisLabel.toLowerCase()}s...`}
            emptyText={`No ${axisLabel.toLowerCase()} values found.`}
          />
        </div>

        {grouped.size === 0 ? (
          <p className="text-sm text-muted-foreground">No hidden sources.</p>
        ) : (
          <div className="space-y-3">
            {[...grouped.entries()].map(([groupAxis, items]) => (
              <div key={groupAxis}>
                <p className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  {AXIS_LABEL[groupAxis] ?? groupAxis}
                </p>
                <div className="flex flex-wrap gap-2">
                  {items.map((r) => (
                    <Badge
                      key={r.id}
                      variant="secondary"
                      className="gap-1 py-1 pl-2.5"
                    >
                      {r.matchValue}
                      <button
                        onClick={() => onRemove(r)}
                        title="Unhide"
                        className="rounded-full p-0.5 hover:bg-background"
                      >
                        <X className="h-3 w-3" />
                      </button>
                    </Badge>
                  ))}
                </div>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function NameRemappingsCard({
  rules,
  onRemove,
}: {
  rules: CurationRule[];
  onRemove: (rule: CurationRule) => void;
}) {
  // Group rename rules by axis (project/language/editor/branch/…).
  const grouped = useMemo(() => {
    const map = new Map<string, CurationRule[]>();
    for (const r of rules) {
      const arr = map.get(r.axis) ?? [];
      arr.push(r);
      map.set(r.axis, arr);
    }
    return map;
  }, [rules]);

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Name remappings</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <p className="text-sm text-muted-foreground">
          Rename or merge values into a single name. Add a rule below, or rename
          a single value from the{" "}
          <Link
            to="/app/heartbeats"
            className="font-medium text-primary hover:underline"
          >
            Heartbeats
          </Link>{" "}
          explorer. Remappings apply to your dashboards at query-time and are
          reversible — raw records are never changed.
        </p>

        <RemappingForm layout="inline" />

        {grouped.size === 0 ? (
          <p className="text-sm text-muted-foreground">No remappings yet.</p>
        ) : (
          <div className="space-y-3">
            {[...grouped.entries()].map(([groupAxis, items]) => (
              <div key={groupAxis}>
                <p className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  {axisLabel(groupAxis as HeartbeatAxis)}
                </p>
                <div className="space-y-1.5">
                  {items.map((r) => (
                    <RemappingRow key={r.id} rule={r} onRemove={onRemove} />
                  ))}
                </div>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function RemappingRow({
  rule,
  onRemove,
}: {
  rule: CurationRule;
  onRemove: (rule: CurationRule) => void;
}) {
  const [open, setOpen] = useState(false);
  // Badge for non-exact rules ("regex" / "template" capture rules).
  const modeBadge =
    rule.matchType === "regex"
      ? "regex"
      : rule.matchType === "template"
        ? "capture"
        : null;

  const affected = useQuery({
    queryKey: ["curation-affected", rule.id],
    queryFn: () => api.getCurationRuleAffected(rule.id),
    enabled: open,
    staleTime: 30_000,
  });

  const total = useMemo(
    () => (affected.data?.values ?? []).reduce((s, v) => s + v.count, 0),
    [affected.data],
  );

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
