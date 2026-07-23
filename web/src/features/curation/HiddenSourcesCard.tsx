import { useMemo, useState } from "react";
import { Combobox } from "@/components/ui/combobox";
import { Card, CardContent, CardHeader, CardTitle } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { AxisSelect } from "@/features/rules/AxisSelect";
import { HiddenValueBadge } from "@/features/curation/HiddenValueBadge";
import { groupByAxis } from "@/features/curation/groupByAxis";
import { axisLabel } from "@/lib/axes";
import { useAxisValues } from "@/features/rules/useAxisValues";
import type { CurationRule, HeartbeatAxis } from "@/types/api";

// Axes exposed in the "hidden sources" picker.
const SOURCE_AXES: readonly HeartbeatAxis[] = ["editor", "plugin", "machine"];

export function HiddenSourcesCard({
  rules,
  onAdd,
  onRemove,
}: {
  rules: CurationRule[];
  onAdd: (axis: string, value: string) => void;
  onRemove: (rule: CurationRule) => void;
}) {
  const [axis, setAxis] = useState<HeartbeatAxis>(SOURCE_AXES[0]);
  const { options, isLoading } = useAxisValues(axis);

  const grouped = useMemo(() => groupByAxis(rules), [rules]);

  const axisName = axisLabel(axis).toLowerCase();

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
          <AxisSelect
            axes={SOURCE_AXES}
            value={axis}
            onChange={setAxis}
            label={null}
            size="default"
            triggerClassName="w-32 shrink-0"
          />
          <Combobox
            options={available}
            value={null}
            onSelect={(v) => onAdd(axis, v)}
            loading={isLoading}
            placeholder={`Select a ${axisName} to hide...`}
            searchPlaceholder={`Search ${axisName}s...`}
            emptyText={`No ${axisName} values found.`}
          />
        </div>

        {grouped.size === 0 ? (
          <p className="text-sm text-muted-foreground">No hidden sources.</p>
        ) : (
          <div className="space-y-3">
            {[...grouped.entries()].map(([groupAxis, items]) => (
              <div key={groupAxis}>
                <p className="mb-1.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                  {axisLabel(groupAxis as HeartbeatAxis)}
                </p>
                <div className="flex flex-wrap gap-2">
                  {items.map((r) => (
                    <HiddenValueBadge
                      key={r.id}
                      value={r.matchValue}
                      onRemove={() => onRemove(r)}
                    />
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
