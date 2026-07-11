import { useMemo } from "react";
import { Link } from "react-router";
import { toast } from "sonner";
import { PageToolbar } from "@/components/toolbar/PageToolbar";
import { Spinner } from "@/components/Spinner";
import { HiddenProjectsCard } from "@/features/curation/HiddenProjectsCard";
import { HiddenSourcesCard } from "@/features/curation/HiddenSourcesCard";
import { NameRemappingsCard } from "@/features/curation/NameRemappingsCard";
import { useCurationMutations, useCurationRules } from "@/features/curation/useCuration";
import type { CurationRule } from "@/types/api";

export function Settings() {
  const { data, isLoading } = useCurationRules();
  const { add, remove } = useCurationMutations();

  const hides = useMemo(
    () => (data ?? []).filter((r) => r.action === "hide"),
    [data],
  );
  const hiddenProjects = hides.filter((r) => r.axis === "project");
  const hiddenSources = hides.filter((r) => r.axis !== "project");

  const renames = useMemo(
    () => (data ?? []).filter((r) => r.action === "rename"),
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
