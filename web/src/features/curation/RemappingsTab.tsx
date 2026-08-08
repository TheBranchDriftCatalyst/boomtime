import { useMemo, useState } from "react";
import { Link } from "react-router";
import { toast } from "sonner";
import { Spinner } from "@thebranchdriftcatalyst/catalyst-ui/ui/spinner";
import { DestructiveActionDialog } from "@/features/curation/DestructiveActionDialog";
import { NameRemappingsCard } from "@/features/curation/NameRemappingsCard";
import {
  useCurationMutations,
  useCurationRules,
} from "@/features/curation/useCuration";
import type { CurationRule } from "@/types/api";

// The "Remappings" Settings tab: curation rules with per-row destructive
// actions.
//
// gaka-cr4 + gaka-due: rows can be rename OR hide rules; the row icons
// dispatch on rule.action. Zap opens the apply modal (rename → rewrite raw
// heartbeats + delete the rule); Trash2 opens the purge modal (hide →
// delete raw heartbeats + delete the rule). ONE DestructiveActionDialog
// instance handles both variants — the variant tag is stashed alongside
// the rule id so a single mount serves every row.
export function RemappingsTab() {
  const { data, isLoading } = useCurationRules();
  const { remove } = useCurationMutations();

  // The destructive modal's state: which rule + which variant (apply | purge).
  // Kept as a single {rule, variant} pair so React sees one state atom and
  // there's no partial-transition window where "which modal am I?" is
  // ambiguous.
  const [pending, setPending] = useState<{
    rule: CurationRule;
    variant: "apply" | "purge";
  } | null>(null);

  // All rules — both renames and hides get a full row (with edit + destructive
  // + remove icons). Hides used to render as small badges in the Curation
  // tab; promoting them to rows here lets us surface the purge action next
  // to the same visual affordances renames get.
  const rules = useMemo(() => data ?? [], [data]);

  function removeRule(rule: CurationRule) {
    remove.mutate(rule.id, {
      onSuccess: () => {
        const label =
          rule.action === "hide"
            ? `Unhid ${rule.matchValue}`
            : `Removed remapping ${rule.matchValue} → ${rule.newValue}`;
        toast.success(label);
      },
      onError: () => toast.error("Failed to remove rule"),
    });
  }

  if (isLoading) return <Spinner />;

  return (
    <div className="space-y-6">
      <p className="text-sm text-muted-foreground">
        Renames and hides are reversible, query-time{" "}
        <span className="font-medium text-foreground">view</span> rules by
        default. To create or merge values, use the{" "}
        <Link
          to="/app/heartbeats"
          className="font-medium text-primary hover:underline"
        >
          Heartbeats
        </Link>{" "}
        explorer. A rule flagged{" "}
        <span className="font-medium text-foreground">Apply at ingest</span>{" "}
        (sky <span className="uppercase">ingest</span> badge) additionally
        scrubs newly-stored heartbeats — going forward, not retroactively. To
        permanently collapse a rule into existing raw data — rewrite matching
        heartbeats (lightning-bolt on renames) or delete them (trashcan on
        hides) — click the destructive icon on the row.
      </p>
      <NameRemappingsCard
        rules={rules}
        onRemove={removeRule}
        onApply={(rule) => setPending({ rule, variant: "apply" })}
        onPurge={(rule) => setPending({ rule, variant: "purge" })}
      />
      <DestructiveActionDialog
        rule={pending?.rule ?? null}
        variant={pending?.variant ?? "apply"}
        onClose={() => setPending(null)}
      />
    </div>
  );
}
