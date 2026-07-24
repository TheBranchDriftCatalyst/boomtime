import { useMemo, useState } from "react";
import { Link } from "react-router";
import { toast } from "sonner";
import { Spinner } from "@/components/Spinner";
import { ApplyMappingDialog } from "@/features/curation/ApplyMappingDialog";
import { NameRemappingsCard } from "@/features/curation/NameRemappingsCard";
import {
  useCurationMutations,
  useCurationRules,
} from "@/features/curation/useCuration";
import type { CurationRule } from "@/types/api";

// The "Remappings" Settings tab: query-time rename rules.
//
// gaka-cr4: the "apply" action per row opens ApplyMappingDialog, which is a
// destructive confirm modal for permanently collapsing a rename mapping into
// the raw data (rewriting matching rows + removing the mapping row). Modal
// state lives here so one dialog instance is reused across rows.
export function RemappingsTab() {
  const { data, isLoading } = useCurationRules();
  const { remove } = useCurationMutations();
  const [applyRule, setApplyRule] = useState<CurationRule | null>(null);

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
        explorer. To permanently collapse a mapping into the raw data (rewrite
        matching heartbeats and remove the mapping), use the lightning-bolt
        icon on each row.
      </p>
      <NameRemappingsCard
        rules={renames}
        onRemove={removeRename}
        onApply={setApplyRule}
      />
      <ApplyMappingDialog
        rule={applyRule}
        onClose={() => setApplyRule(null)}
      />
    </div>
  );
}
