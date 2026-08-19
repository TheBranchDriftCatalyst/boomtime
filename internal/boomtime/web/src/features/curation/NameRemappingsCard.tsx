import { useMemo } from "react";
import { Link } from "react-router";
import { Card, CardContent, CardHeader, CardTitle } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import { RemappingForm } from "@boomtime/features/curation/RemappingForm";
import { RemappingRow } from "@boomtime/features/curation/RemappingRow";
import { groupByAxis } from "@boomtime/features/curation/groupByAxis";
import { axisLabel } from "@shared/lib/axes";
import type { CurationRule, HeartbeatAxis } from "@shared/types/api";

export function NameRemappingsCard({
  rules,
  onRemove,
  onApply,
  onPurge,
}: {
  rules: CurationRule[];
  onRemove: (rule: CurationRule) => void;
  // gaka-cr4: opens the destructive-apply modal (rename rules only).
  onApply?: (rule: CurationRule) => void;
  // gaka-due: opens the destructive-purge modal (hide rules only).
  onPurge?: (rule: CurationRule) => void;
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
          explorer. By default a remapping is a reversible{" "}
          <span className="font-medium text-foreground">view</span> rule —
          applied to your dashboards at query-time, raw records untouched. Turn
          on{" "}
          <span className="font-medium text-foreground">Apply at ingest</span>{" "}
          to also scrub new heartbeats as they're stored; those rows carry an{" "}
          <span className="rounded border border-sky-500/40 px-1 text-[10px] uppercase text-sky-400">
            ingest
          </span>{" "}
          badge and the rewrite is irreversible for new data.
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
                    <RemappingRow
                      key={r.id}
                      rule={r}
                      onRemove={onRemove}
                      onApply={onApply}
                      onPurge={onPurge}
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
