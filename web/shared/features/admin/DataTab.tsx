// DataTab — Admin > Data (gaka-5jp follow-up). Derived-data / rollup health:
// heartbeat vs rollup counts, drift, table + database sizes, index breakdown,
// and the Resync action. Moved here from the Heartbeats page header — it's
// operator/observability info, not part of per-page heartbeat browsing.
//
// Renders through the shared AdminTabShell base (gaka-zp2s) for a consistent
// admin-tab title + wrapper; DerivedStatusPanel owns its own load/error states.
import { AdminTabShell } from "@shared/shared/admin/AdminTabShell";
import { DerivedStatusPanel } from "@shared/features/admin/DerivedStatusPanel";

export function DataTab() {
  return (
    <AdminTabShell
      title="Storage & derived data"
      description="Heartbeat vs rollup health, table + database sizes, index breakdown, and resync."
      bodyClassName="max-w-5xl space-y-4"
    >
      <DerivedStatusPanel />
    </AdminTabShell>
  );
}
