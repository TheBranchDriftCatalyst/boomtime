import { useMemo } from "react";
import { Link } from "react-router";
import { Card, CardContent, CardHeader, CardTitle } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { RemappingForm } from "@/features/curation/RemappingForm";
import { RemappingRow } from "@/features/curation/RemappingRow";
import { groupByAxis } from "@/features/curation/groupByAxis";
import { axisLabel } from "@/lib/axes";
import type { CurationRule, HeartbeatAxis } from "@/types/api";

export function NameRemappingsCard({
  rules,
  onRemove,
}: {
  rules: CurationRule[];
  onRemove: (rule: CurationRule) => void;
}) {
  // Group rename rules by axis (project/language/editor/branch/…).
  const grouped = useMemo(() => groupByAxis(rules), [rules]);

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
