// Core (domain-agnostic) registration module.
//
// Registers the FE surface that belongs to NO single domain: the mixed-fusion
// Overview, the app shell + auth/public routes, the Settings entry, the
// account-level settings tabs, and the operational admin tabs (users / data /
// jobs / cli / logs).
//
// Like every domain module it imports the shell's register API + its own things
// and NOTHING from another domain — the host entry decides it runs (see
// web/src/app/registerDomains.ts). CRITICAL: registration must stay side-effect
// free — every route/tab body is lazy() so NO feature/shell module is evaluated
// merely by registering (the test setup registers all domains up front; eager
// imports here would pull heavy chains into the pre-test module graph and break
// per-file vi.mock isolation). Each lazy() also keeps its own code-split chunk,
// so the app-entry bundle stays lean.
import { lazy, Suspense } from "react";
import { Navigate } from "react-router";
import { LayoutDashboard, Settings2 } from "lucide-react";

import { registerNavItem } from "@shared/shared/nav/registry";
import { registerSettingsSection } from "@shared/shared/settings/registry";
import { registerAdminTab } from "@shared/shared/admin/registry";
import { registerRoute } from "@shared/shared/routing/registry";
import { PageFallback } from "@shared/shared/routing/PageFallback";
import { IS_BOOKS_STANDALONE } from "@shared/lib/standalone";

const ProfileTab = lazy(() =>
  import("./AccountTabs").then((m) => ({ default: m.ProfileTab })),
);
const PluginAndTokensTab = lazy(() =>
  import("./AccountTabs").then((m) => ({ default: m.PluginAndTokensTab })),
);
const Changelog = lazy(() =>
  import("@shared/features/changelog/Changelog").then((m) => ({
    default: m.Changelog,
  })),
);

// ── Route bodies (all lazy — one chunk per import site, zero eager eval) ───
const RootRedirect = lazy(() =>
  import("./RootRedirect").then((m) => ({ default: m.RootRedirect })),
);
const Login = lazy(() =>
  import("@shared/features/auth/Login").then((m) => ({ default: m.Login })),
);
const Register = lazy(() =>
  import("@shared/features/auth/Register").then((m) => ({ default: m.Register })),
);
const Onboarding = lazy(() =>
  import("@boomtime/features/onboarding/Onboarding").then((m) => ({
    default: m.Onboarding,
  })),
);
const EditableProfilePage = lazy(() =>
  import("@shared/features/publicprofile/EditableProfilePage").then((m) => ({
    default: m.EditableProfilePage,
  })),
);
// The "/app" shell: ProtectedRoute + AppShell — lazy so its whole layout/
// sidebar chain stays out of the entry bundle AND out of the test module graph.
const ProtectedRoute = lazy(() =>
  import("@shared/app/ProtectedRoute").then((m) => ({ default: m.ProtectedRoute })),
);
const AppShell = lazy(() =>
  import("@shared/layout/AppShell").then((m) => ({ default: m.AppShell })),
);
const AdminRoute = lazy(() =>
  import("@shared/app/AdminRoute").then((m) => ({ default: m.AdminRoute })),
);
const AppShellRoute = () => (
  <ProtectedRoute>
    <AppShell />
  </ProtectedRoute>
);
const Overview = lazy(() =>
  import("@shared/features/overview/Overview").then((m) => ({ default: m.Overview })),
);
const InAppProfile = lazy(() =>
  import("@shared/features/publicprofile/InAppProfilePage").then((m) => ({
    default: m.InAppProfilePage,
  })),
);
const Settings = lazy(() =>
  import("@shared/features/settings/Settings").then((m) => ({ default: m.Settings })),
);
const AdminPage = lazy(() =>
  import("@shared/features/admin/AdminPage").then((m) => ({ default: m.AdminPage })),
);
const AdminShellRoute = () => (
  <AdminRoute>
    <AdminPage />
  </AdminRoute>
);
const UsersTab = lazy(() =>
  import("@shared/features/admin/UsersTab").then((m) => ({ default: m.UsersTab })),
);
const DataTab = lazy(() =>
  import("@shared/features/admin/DataTab").then((m) => ({ default: m.DataTab })),
);
const CliTab = lazy(() =>
  import("@shared/features/admin/CliTab").then((m) => ({ default: m.CliTab })),
);
const JobsTab = lazy(() =>
  import("@shared/features/admin/JobsTab").then((m) => ({ default: m.JobsTab })),
);
const Logs = lazy(() =>
  import("@shared/features/logs/Logs").then((m) => ({ default: m.Logs })),
);

export function registerCoreDomain(): void {
  // ── Nav ────────────────────────────────────────────────────────────────
  // Overview: the mixed-fusion dashboard, first + ungrouped (it fuses BOTH
  // domains, so it deliberately isn't bucketed under one). HOST-ONLY: it fuses
  // the code + books domains, so the books-only standalone (which never
  // registers the code domain) has nothing to fuse — its nav is Books +
  // Settings + Profile, and /app redirects straight to /app/books (below).
  if (!IS_BOOKS_STANDALONE) {
    registerNavItem(
      { id: "core", order: 0 },
      { name: "Overview", icon: LayoutDashboard, to: "/app", end: true },
    );
  }
  // Settings: domain-agnostic account/config entry (Logs + Changelog live in it).
  registerNavItem(
    { id: "config", order: 90 },
    { name: "Settings", icon: Settings2, to: "/app/settings" },
  );

  // ── Routes ─────────────────────────────────────────────────────────────
  // Top-level (pre-/outside-app) routes + the "/app" shell wrapper that every
  // domain's app pages nest under. Domains register their leaves with
  // parent: "app" (or parent: "admin" for the admin sub-shell below).
  registerRoute({
    path: "/",
    element: (
      <Suspense fallback={<PageFallback />}>
        <RootRedirect />
      </Suspense>
    ),
    order: 0,
  });
  registerRoute({
    path: "/login",
    element: (
      <Suspense fallback={<PageFallback />}>
        <Login />
      </Suspense>
    ),
    order: 10,
  });
  registerRoute({
    path: "/register",
    element: (
      <Suspense fallback={<PageFallback />}>
        <Register />
      </Suspense>
    ),
    order: 20,
  });
  // Beta onboarding preview (gaka-93f.1.2): reached via BetaOnboardingGate when
  // ?enable_beta_user_registration=true is set.
  registerRoute({
    path: "/onboarding",
    element: (
      <Suspense fallback={<PageFallback />}>
        <Onboarding />
      </Suspense>
    ),
    order: 30,
  });
  // Public profile — anonymous for visitors, editable for owners (gaka-ie3).
  registerRoute({
    path: "/p/:slug",
    element: (
      <Suspense fallback={<PageFallback />}>
        <EditableProfilePage />
      </Suspense>
    ),
    order: 40,
  });
  // The "/app" shell: ProtectedRoute + AppShell. id "app" is the mount point the
  // other domains hang their pages off of.
  registerRoute({
    id: "app",
    path: "/app",
    element: (
      <Suspense fallback={<PageFallback />}>
        <AppShellRoute />
      </Suspense>
    ),
    order: 60,
  });
  // SPA catch-all — unknown paths bounce to "/" (which then routes by auth).
  registerRoute({ path: "*", element: <Navigate to="/" replace />, order: 1000 });

  // ── /app leaves (core) ─────────────────────────────────────────────────
  // The /app index: HOST renders the mixed-fusion Overview. The books-only
  // STANDALONE has no Overview (it fuses the code domain, which isn't
  // registered here), so /app redirects to the Books library — the app's real
  // home — and the Overview module never enters the books entry graph.
  registerRoute({
    parent: "app",
    index: true,
    element: IS_BOOKS_STANDALONE ? (
      <Navigate to="/app/books" replace />
    ) : (
      <Suspense fallback={<PageFallback />}>
        <Overview />
      </Suspense>
    ),
    order: 0,
  });
  // gaka-4ng: owner's profile inside the app skeleton.
  registerRoute({
    parent: "app",
    path: "profile",
    element: (
      <Suspense fallback={<PageFallback />}>
        <InAppProfile />
      </Suspense>
    ),
    order: 20,
  });
  // gaka-ebq: Logs moved under /app/admin/logs; keep the old bookmarkable URL.
  registerRoute({
    parent: "app",
    path: "logs",
    element: <Navigate to="/app/admin/logs" replace />,
    order: 90,
  });
  // Changelog still ships as a Settings tab.
  registerRoute({
    parent: "app",
    path: "changelog",
    element: <Navigate to="/app/settings?tab=changelog" replace />,
    order: 100,
  });
  // /app/admin — admin-only sub-shell (AdminRoute + AdminPage). id "admin" is
  // the mount point for the per-domain admin tabs.
  registerRoute({
    id: "admin",
    parent: "app",
    path: "admin",
    element: (
      <Suspense fallback={<PageFallback />}>
        <AdminShellRoute />
      </Suspense>
    ),
    order: 110,
  });
  registerRoute({
    parent: "app",
    path: "settings",
    element: (
      <Suspense fallback={<PageFallback />}>
        <Settings />
      </Suspense>
    ),
    order: 120,
  });

  // ── /app/admin leaves (core / operational) ─────────────────────────────
  registerRoute({
    parent: "admin",
    index: true,
    element: <Navigate to="/app/admin/labels" replace />,
    order: 0,
  });
  registerRoute({
    parent: "admin",
    path: "users",
    element: (
      <Suspense fallback={<PageFallback />}>
        <UsersTab />
      </Suspense>
    ),
    order: 10,
  });
  registerRoute({
    parent: "admin",
    path: "cli",
    element: (
      <Suspense fallback={<PageFallback />}>
        <CliTab />
      </Suspense>
    ),
    order: 30,
  });
  registerRoute({
    parent: "admin",
    path: "jobs",
    element: (
      <Suspense fallback={<PageFallback />}>
        <JobsTab />
      </Suspense>
    ),
    order: 40,
  });
  registerRoute({
    parent: "admin",
    path: "data",
    element: (
      <Suspense fallback={<PageFallback />}>
        <DataTab />
      </Suspense>
    ),
    order: 60,
  });
  registerRoute({
    parent: "admin",
    path: "logs",
    element: (
      <Suspense fallback={<PageFallback />}>
        {/* embedded so the AdminPage's toolbar/tab-strip stays the single page
            heading — Logs otherwise renders its own PageToolbar title. */}
        <Logs embedded />
      </Suspense>
    ),
    order: 80,
  });

  // ── Settings (Account group) ───────────────────────────────────────────
  registerSettingsSection({
    id: "account",
    label: "Account",
    order: 0,
    tabs: [
      { id: "profile", label: "Profile", order: 0, render: () => <ProfileTab /> },
      {
        id: "plugin",
        label: "Plugin & tokens",
        order: 1,
        render: () => <PluginAndTokensTab />,
      },
      {
        id: "changelog",
        label: "Changelog",
        order: 2,
        render: () => <Changelog embedded />,
      },
    ],
  });

  // ── Admin (core / operational group) ───────────────────────────────────
  const core = { id: "core", label: "Operations", order: 0 };
  registerAdminTab({ id: "users", label: "Users", to: "/app/admin/users", group: core, order: 0 });
  registerAdminTab({ id: "data", label: "Data", to: "/app/admin/data", group: core, order: 1 });
  registerAdminTab({ id: "jobs", label: "Jobs", to: "/app/admin/jobs", group: core, order: 2 });
  registerAdminTab({ id: "cli", label: "Commands", to: "/app/admin/cli", group: core, order: 3 });
  registerAdminTab({ id: "logs", label: "Logs", to: "/app/admin/logs", group: core, order: 4 });
}
