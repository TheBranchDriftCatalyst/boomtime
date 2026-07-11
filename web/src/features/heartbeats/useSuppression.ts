import { useCallback, useMemo } from "react";
import { toast } from "sonner";
import { isSuppressibleAxis } from "@/features/heartbeats/axes";
import { useCurationMutations, useCurationRules } from "@/features/curation/useCuration";
import { remapDisplay } from "@/features/curation/remapDisplay";
import type { GroupNode } from "@/features/heartbeats/explorerModel";

// Result of looking up a group's suppression state.
export interface SuppressInfo {
  suppressible: boolean;
  ruleId: number | null; // non-null => currently suppressed (active hide rule)
}

/**
 * Suppression = the reversible curation HIDE. Reuses the same rules Settings
 * uses so a project suppressed in the Explorer shows up there (and
 * vice-versa). Also exposes what a group's raw value remaps to in the
 * dashboards, since renames are read from the same curation rule set.
 */
export function useSuppression() {
  const { data: curation } = useCurationRules();
  const { add: addRule, remove: removeRule } = useCurationMutations();

  // Fast lookup of active hide rules by "axis\u0000value".
  const hideRuleByKey = useMemo(() => {
    const m = new Map<string, number>();
    for (const r of curation ?? []) {
      if (r.action === "hide") m.set(`${r.axis}\u0000${r.matchValue}`, r.id);
    }
    return m;
  }, [curation]);

  // What a group's raw value remaps to in the dashboards, covering exact,
  // regex, AND template rename rules (shared with the RemappingForm preview).
  const getRenamedTo = useCallback(
    (n: GroupNode): string | null =>
      remapDisplay(n.axis, n.value, curation),
    [curation],
  );

  const getSuppressInfo = useCallback(
    (n: GroupNode): SuppressInfo => {
      if (n.value == null || !isSuppressibleAxis(n.axis)) {
        return { suppressible: false, ruleId: null };
      }
      const ruleId = hideRuleByKey.get(`${n.axis}\u0000${n.value}`) ?? null;
      return { suppressible: true, ruleId };
    },
    [hideRuleByKey],
  );

  const toggleSuppress = useCallback(
    (n: GroupNode, info: SuppressInfo) => {
      const value = n.value as string;
      if (info.ruleId != null) {
        removeRule.mutate(info.ruleId, {
          onSuccess: () => toast.success(`Unsuppressed "${value}"`),
          onError: () => toast.error("Failed to unsuppress"),
        });
      } else {
        addRule.mutate(
          { axis: n.axis, action: "hide", matchValue: value },
          {
            onSuccess: () =>
              toast.success(
                `Suppressed "${value}" — hidden from dashboards (still shown here in the audit)`,
              ),
            onError: () => toast.error("Failed to suppress"),
          },
        );
      }
    },
    [addRule, removeRule],
  );

  return {
    getSuppressInfo,
    getRenamedTo,
    toggleSuppress,
    suppressBusy: addRule.isPending || removeRule.isPending,
  };
}
