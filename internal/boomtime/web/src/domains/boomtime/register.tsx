// Boomtime (code / wakatime analytics) domain registration module.
//
// The code-domain FE: the analytics nav pages + their routes, the wakatime-
// shaped settings (hidden data / remappings / widgets), and the code-domain
// admin tabs (label images + rate metrics). Imports only the shell register API
// + its own components — a books-only build never reaches this module, so its
// whole component graph (Projects / Leaderboards / Heartbeats / Wellness / …)
// tree-shakes away, router table included.
import { lazy, Suspense } from "react";
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
import { registerRoute } from "@/shared/routing/registry";
import { PageFallback } from "@/shared/routing/PageFallback";

// Settings tab bodies are lazy() so registration is cheap + side-effect free —
// each keeps its own code-split chunk (as it had inside the lazy Settings page).
const CurationTab = lazy(() =>
  import("@boomtime/features/curation/CurationTab").then((m) => ({
    default: m.CurationTab,
  })),
);
const RemappingsTab = lazy(() =>
  import("@boomtime/features/curation/RemappingsTab").then((m) => ({
    default: m.RemappingsTab,
  })),
);
const WidgetLinksCard = lazy(() =>
  import("@/features/widgets/WidgetLinksCard").then((m) => ({
    default: m.WidgetLinksCard,
  })),
);

// ── Route page bodies (lazy — one chunk per import site) ──────────────────
const Projects = lazy(() =>
  import("@boomtime/features/projects/Projects").then((m) => ({ default: m.Projects })),
);
const Leaderboards = lazy(() =>
  import("@boomtime/features/leaderboards/Leaderboards").then((m) => ({
    default: m.Leaderboards,
  })),
);
const Heartbeats = lazy(() =>
  import("@boomtime/features/heartbeats/Heartbeats").then((m) => ({
    default: m.Heartbeats,
  })),
);
const SpaceView = lazy(() =>
  import("@boomtime/features/spaces/SpaceView").then((m) => ({ default: m.SpaceView })),
);
const Import = lazy(() =>
  import("@boomtime/features/import/Import").then((m) => ({ default: m.Import })),
);
const Wellness = lazy(() =>
  import("@boomtime/features/wellness/Wellness").then((m) => ({ default: m.Wellness })),
);
// gaka-gud: Goals promoted from a Settings sub-tab to a top-level page.
const Goals = lazy(() =>
  import("@boomtime/features/goals/Goals").then((m) => ({ default: m.Goals })),
);
// Widget catalog gallery — one lazy component, two routes: authed
// /app/catalog (variant="app") and public /catalog (variant="public").
const CatalogPage = lazy(() =>
  import("@boomtime/features/catalog/CatalogPage").then((m) => ({
    default: m.CatalogPage,
  })),
);
// Admin code-domain tabs.
const AdminTab = lazy(() =>
  import("@/features/admin/AdminTab").then((m) => ({ default: m.AdminTab })),
);
const MetricsTab = lazy(() =>
  import("@boomtime/features/admin/MetricsTab").then((m) => ({ default: m.MetricsTab })),
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

  // ── Routes ─────────────────────────────────────────────────────────────
  // Public widget catalog — unauthed, sample data only, own minimal shell
  // (like /p/:slug). The authed /app/catalog counterpart lives in the /app
  // tree below with the my-data toggle.
  registerRoute({
    path: "/catalog",
    element: (
      <Suspense fallback={<PageFallback />}>
        <CatalogPage variant="public" />
      </Suspense>
    ),
    order: 50,
  });
  // /app leaves (boomtime code-domain pages).
  registerRoute({
    parent: "app",
    path: "projects",
    element: (
      <Suspense fallback={<PageFallback />}>
        <Projects />
      </Suspense>
    ),
    order: 10,
  });
  // gaka-gud: Goals as a top-level page (moved out of Settings).
  registerRoute({
    parent: "app",
    path: "goals",
    element: (
      <Suspense fallback={<PageFallback />}>
        <Goals />
      </Suspense>
    ),
    order: 30,
  });
  registerRoute({
    parent: "app",
    path: "leaderboards",
    element: (
      <Suspense fallback={<PageFallback />}>
        <Leaderboards />
      </Suspense>
    ),
    order: 50,
  });
  registerRoute({
    parent: "app",
    path: "heartbeats",
    element: (
      <Suspense fallback={<PageFallback />}>
        <Heartbeats />
      </Suspense>
    ),
    order: 60,
  });
  registerRoute({
    parent: "app",
    path: "space/:id",
    element: (
      <Suspense fallback={<PageFallback />}>
        <SpaceView />
      </Suspense>
    ),
    order: 70,
  });
  registerRoute({
    parent: "app",
    path: "import",
    element: (
      <Suspense fallback={<PageFallback />}>
        <Import />
      </Suspense>
    ),
    order: 80,
  });
  registerRoute({
    parent: "app",
    path: "wellness",
    element: (
      <Suspense fallback={<PageFallback />}>
        <Wellness />
      </Suspense>
    ),
    order: 130,
  });
  registerRoute({
    parent: "app",
    path: "catalog",
    element: (
      <Suspense fallback={<PageFallback />}>
        <CatalogPage variant="app" />
      </Suspense>
    ),
    order: 140,
  });
  // /app/admin leaves (boomtime code-domain admin tabs).
  registerRoute({
    parent: "admin",
    path: "labels",
    element: (
      <Suspense fallback={<PageFallback />}>
        <AdminTab />
      </Suspense>
    ),
    order: 20,
  });
  registerRoute({
    parent: "admin",
    path: "metrics",
    element: (
      <Suspense fallback={<PageFallback />}>
        <MetricsTab />
      </Suspense>
    ),
    order: 50,
  });

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
