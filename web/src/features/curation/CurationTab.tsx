import { useMemo } from "react";
import { toast } from "sonner";
import { Spinner } from "@/components/Spinner";
import { HiddenProjectsCard } from "@/features/curation/HiddenProjectsCard";
import { HiddenSourcesCard } from "@/features/curation/HiddenSourcesCard";
import {
  useCurationMutations,
  useCurationRules,
} from "@/features/curation/useCuration";
import type { CurationRule } from "@/types/api";

// The "Hidden data" Settings tab: hide/unhide projects + sources. Renames live
// in the sibling RemappingsTab; both share the same rules query (react-query
// dedupes the fetch).
export function CurationTab() {
  const { data, isLoading } = useCurationRules();
  const { add, remove } = useCurationMutations();

  const hides = useMemo(
    () => (data ?? []).filter((r) => r.action === "hide"),
    [data],
  );
  const hiddenProjects = hides.filter((r) => r.axis === "project");
  const hiddenSources = hides.filter((r) => r.axis !== "project");

  function unhide(rule: CurationRule) {
    remove.mutate(rule.id, {
      onSuccess: () => toast.success(`Unhid ${rule.matchValue}`),
      onError: () => toast.error("Failed to unhide"),
    });
  }

  function addHide(axis: string, value: string) {
    const matchValue = value.trim();
    if (!matchValue) return;
    if (hides.some((r) => r.axis === axis && r.matchValue === matchValue)) {
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

  if (isLoading) return <Spinner />;

  return (
    <div className="space-y-6">
      <p className="text-sm text-muted-foreground">
        Hidden items are excluded from your dashboards but never deleted;
        unhide anytime.
      </p>
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
    </div>
  );
}
