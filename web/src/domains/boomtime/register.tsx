// Boomtime (code / wakatime analytics) domain registration module.
//
// The code-domain FE: the analytics nav pages, the wakatime-shaped settings
// (hidden data / remappings / widgets), and the code-domain admin tabs (label
// images + rate metrics). Imports only the shell register API + its own
// components — a books-only build never reaches this module, so its component
// graph tree-shakes away.
import { lazy } from "react";
import {
  Award,
  BookOpen,
  Download,
  HeartPulse,
  ListTree,
  Shapes,
  Target,
} from "lucide-react";

import { registerNavItem } from "@/shared/nav/registry";
import { registerSettingsSection } from "@/shared/settings/registry";
import { registerAdminTab } from "@/shared/admin/registry";

// Settings tab bodies are lazy() so registration is cheap + side-effect free —
// each keeps its own code-split chunk (as it had inside the lazy Settings page).
const CurationTab = lazy(() =>
  import("@/features/curation/CurationTab").then((m) => ({
    default: m.CurationTab,
  })),
);
const RemappingsTab = lazy(() =>
  import("@/features/curation/RemappingsTab").then((m) => ({
    default: m.RemappingsTab,
  })),
);
const WidgetLinksCard = lazy(() =>
  import("@/features/widgets/WidgetLinksCard").then((m) => ({
    default: m.WidgetLinksCard,
  })),
);

export function registerBoomtimeDomain(): void {
  // ── Nav (grouped under a "Boomtime" header) ────────────────────────────
  const boomtime = { id: "boomtime", label: "Boomtime", order: 20 };
  registerNavItem(boomtime, { name: "Projects", icon: BookOpen, to: "/app/projects", order: 0 });
  registerNavItem(boomtime, { name: "Leaderboards", icon: Award, to: "/app/leaderboards", order: 1 });
  registerNavItem(boomtime, { name: "Goals", icon: Target, to: "/app/goals", order: 2 });
  registerNavItem(boomtime, { name: "Heartbeats", icon: ListTree, to: "/app/heartbeats", order: 3 });
  registerNavItem(boomtime, { name: "Wellness", icon: HeartPulse, to: "/app/wellness", order: 4 });
  registerNavItem(boomtime, { name: "Catalog", icon: Shapes, to: "/app/catalog", order: 5 });
  registerNavItem(boomtime, { name: "Import", icon: Download, to: "/app/import", order: 6 });

  // ── Settings (Boomtime group) ──────────────────────────────────────────
  registerSettingsSection({
    id: "boomtime",
    label: "Boomtime",
    order: 20,
    tabs: [
      { id: "curation", label: "Hidden data", order: 0, render: () => <CurationTab /> },
      { id: "remappings", label: "Remappings", order: 1, render: () => <RemappingsTab /> },
      { id: "widgets", label: "Widgets", order: 2, render: () => <WidgetLinksCard /> },
    ],
  });

  // ── Admin (Boomtime group) ─────────────────────────────────────────────
  const group = { id: "boomtime", label: "Boomtime", order: 20 };
  registerAdminTab({ id: "labels", label: "Labels", to: "/app/admin/labels", group, order: 0 });
  registerAdminTab({ id: "metrics", label: "Metrics", to: "/app/admin/metrics", group, order: 1 });
}
