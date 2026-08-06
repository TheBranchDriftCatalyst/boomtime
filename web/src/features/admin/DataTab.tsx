// DataTab — Admin > Data (gaka-5jp follow-up). Derived-data / rollup health:
// heartbeat vs rollup counts, drift, table + database sizes, index breakdown,
// and the Resync action. Moved here from the Heartbeats page header — it's
// operator/observability info, not part of per-page heartbeat browsing.
import { DerivedStatusPanel } from "@/features/admin/DerivedStatusPanel";

export function DataTab() {
  return (
    <div className="max-w-5xl space-y-4">
      <DerivedStatusPanel />
    </div>
  );
}
