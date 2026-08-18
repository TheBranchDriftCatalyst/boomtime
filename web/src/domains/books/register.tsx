// CatalystBooks domain registration module.
//
// The book bridge + external-account connectors. Registers the Books nav
// destination, the Connections settings tab (all provider links live here), and
// the Books admin tab (source-diagnostics / reading-monitor / raw-feed bridge).
// Imports only the shell register API + its own things; the Connections tab body
// is lazy() so registration stays cheap + side-effect free.
import { lazy } from "react";
import { Library } from "lucide-react";

import { registerNavItem } from "@/shared/nav/registry";
import { registerSettingsSection } from "@/shared/settings/registry";
import { registerAdminTab } from "@/shared/admin/registry";

const ConnectionsTab = lazy(() =>
  import("./ConnectionsTab").then((m) => ({ default: m.ConnectionsTab })),
);

export function registerBooksDomain(): void {
  // ── Nav ────────────────────────────────────────────────────────────────
  // Books is its own top-level destination today, gated on books_enabled so
  // it's fully inert on deployments that don't run the feature.
  registerNavItem(
    { id: "books", order: 10 },
    { name: "Books", icon: Library, to: "/app/books", flag: "books_enabled" },
  );

  // ── Settings (CatalystBooks group) ─────────────────────────────────────
  registerSettingsSection({
    id: "catalystbooks",
    label: "CatalystBooks",
    order: 10,
    tabs: [
      { id: "connections", label: "Connections", order: 0, render: () => <ConnectionsTab /> },
    ],
  });

  // ── Admin (CatalystBooks group) ────────────────────────────────────────
  registerAdminTab({
    id: "books",
    label: "Books",
    to: "/app/admin/books",
    group: { id: "catalystbooks", label: "CatalystBooks", order: 10 },
    order: 0,
  });
}
