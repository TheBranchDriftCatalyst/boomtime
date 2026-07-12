import { useMemo } from "react";
import { Link } from "react-router";
import { toast } from "sonner";
import { Spinner } from "@/components/Spinner";
import { NameRemappingsCard } from "@/features/curation/NameRemappingsCard";
import {
  useCurationMutations,
  useCurationRules,
} from "@/features/curation/useCuration";
import type { CurationRule } from "@/types/api";

// The "Remappings" Settings tab: query-time rename rules.
export function RemappingsTab() {
  const { data, isLoading } = useCurationRules();
  const { remove } = useCurationMutations();

  const renames = useMemo(
    () => (data ?? []).filter((r) => r.action === "rename"),
    [data],
  );

  function removeRename(rule: CurationRule) {
    remove.mutate(rule.id, {
      onSuccess: () =>
        toast.success(`Removed remapping ${rule.matchValue} → ${rule.newValue}`),
      onError: () => toast.error("Failed to remove remapping"),
    });
  }

  if (isLoading) return <Spinner />;

  return (
    <div className="space-y-6">
      <p className="text-sm text-muted-foreground">
        Renames are reversible, query-time remaps. To create or merge values,
        use the{" "}
        <Link
          to="/app/heartbeats"
          className="font-medium text-primary hover:underline"
        >
          Heartbeats
        </Link>{" "}
        explorer.
      </p>
      <NameRemappingsCard rules={renames} onRemove={removeRename} />
    </div>
  );
}
