import { useMemo } from "react";
import { Combobox } from "@/components/ui/combobox";
import { Card, CardContent, CardHeader, CardTitle } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { HiddenValueBadge } from "@/features/curation/HiddenValueBadge";
import { useAxisValues } from "@/features/rules/useAxisValues";
import type { CurationRule } from "@/types/api";

export function HiddenProjectsCard({
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
              <HiddenValueBadge
                key={r.id}
                value={r.matchValue}
                onRemove={() => onRemove(r)}
              />
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
