import { lazy, Suspense, useEffect } from "react";
import {
  Navigate,
  Outlet,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from "react-router";
import { AppShell } from "@/layout/AppShell";
import { ProtectedRoute } from "@/app/ProtectedRoute";
import { AdminRoute } from "@/app/AdminRoute";
import { useAuth } from "@/features/auth/useAuth";
import { Spinner } from "@thebranchdriftcatalyst/catalyst-ui/ui/spinner";
import { AnalyticsTracker } from "@/app/AnalyticsTracker";
import { AuthProvider } from "@/features/auth/useAuth";
// Auth pages are eagerly imported: the pre-auth bundle is tiny and Login is
// the most-common landing page after a fresh visit — code-splitting it just
// costs a network round-trip on the critical path.
import { Login } from "@/features/auth/Login";
import { Register } from "@/features/auth/Register";
import { Onboarding } from "@/features/onboarding/Onboarding";
import { useBetaRegistration } from "@/features/onboarding/betaRegistration";

// gaka-4hv: split each authed feature into its own chunk so the initial JS
// download is the shell + auth + shared vendor libs, not every dashboard viz
// bundled together. React.lazy + <Suspense> covers the wait; Vite (rolldown)
// emits one chunk per lazy() import site.
const Overview = lazy(() =>
  import("@/features/overview/Overview").then((m) => ({ default: m.Overview })),
);
const Projects = lazy(() =>
  import("@/features/projects/Projects").then((m) => ({ default: m.Projects })),
);
const Leaderboards = lazy(() =>
  import("@/features/leaderboards/Leaderboards").then((m) => ({
    default: m.Leaderboards,
  })),
);
const Heartbeats = lazy(() =>
  import("@/features/heartbeats/Heartbeats").then((m) => ({
    default: m.Heartbeats,
  })),
);
const SpaceView = lazy(() =>
  import("@/features/spaces/SpaceView").then((m) => ({ default: m.SpaceView })),
);
const Import = lazy(() =>
  import("@/features/import/Import").then((m) => ({ default: m.Import })),
);
const Settings = lazy(() =>
  import("@/features/settings/Settings").then((m) => ({ default: m.Settings })),
);
const Wellness = lazy(() =>
  import("@/features/wellness/Wellness").then((m) => ({ default: m.Wellness })),
);
// gaka-gud: Goals promoted from a Settings sub-tab to a top-level page.
const Goals = lazy(() =>
  import("@/features/goals/Goals").then((m) => ({ default: m.Goals })),
);
// gaka-4ng: the owner's profile mounted INSIDE the app skeleton (/app/profile).
// Reuses the dossier view + editor but always-owner (no :slug param). The
// standalone public /p/:slug (EditableProfilePage) stays for external visitors.
const InAppProfile = lazy(() =>
  import("@/features/publicprofile/InAppProfilePage").then((m) => ({
    default: m.InAppProfilePage,
  })),
);
// gaka-ebq: Admin section is its own chunk. Non-admins never trigger the
// fetch because the sidebar link is hidden AND AdminRoute short-circuits
// to /app before <Suspense> ever kicks. The three tab bodies also lazy —
// operators rarely tap all three in one visit.
const AdminPage = lazy(() =>
  import("@/features/admin/AdminPage").then((m) => ({ default: m.AdminPage })),
);
const AdminTab = lazy(() =>
  import("@/features/admin/AdminTab").then((m) => ({ default: m.AdminTab })),
);
const BackfillTab = lazy(() =>
  import("@/features/admin/BackfillTab").then((m) => ({
    default: m.BackfillTab,
  })),
);
const UsersTab = lazy(() =>
  import("@/features/admin/UsersTab").then((m) => ({ default: m.UsersTab })),
);
// gaka-gud follow-up: derived-data / storage health moved off Heartbeats.
const DataTab = lazy(() =>
  import("@/features/admin/DataTab").then((m) => ({ default: m.DataTab })),
);
const Logs = lazy(() =>
  import("@/features/logs/Logs").then((m) => ({ default: m.Logs })),
);
// Public profile lives OUTSIDE the /app tree — /p/:slug is unauthenticated
// for anonymous visitors and renders its own minimal shell (no sidebar,
// no header). gaka-ie3: the route now points at EditableProfilePage which
// dispatches to the read-only PublicDashboard for non-owners and to the
// inline editor for the caller-owns-this-profile case. Non-owner paths
// are byte-identical to the previous behavior.
const EditableProfilePage = lazy(() =>
  import("@/features/publicprofile/EditableProfilePage").then((m) => ({
    default: m.EditableProfilePage,
  })),
);

function RootRedirect() {
  const { isLoggedIn, bootstrapping } = useAuth();
  if (bootstrapping) {
    return (
      <div className="flex h-screen items-center justify-center">
        <Spinner />
      </div>
    );
  }
  return <Navigate to={isLoggedIn ? "/app" : "/login"} replace />;
}

// PageFallback is the Suspense placeholder for the chunk fetch — same shape
// as the router's own bootstrap Spinner so a route switch and initial boot
// look identical.
function PageFallback() {
  return (
    <div className="flex h-[60vh] items-center justify-center">
      <Spinner />
    </div>
  );
}

// gaka-ie3: split into two exports for the data-router migration.
// `RootLayout` is the top-level route element mounted by
// createBrowserRouter — it owns providers that historically lived in
// main.tsx (AuthProvider + AnalyticsTracker) but need access to the
// router context. `AppRoutes` is the leaf that renders the classic
// nested <Routes> tree; lazy-loaded by the root config so future
// per-route lazy-loading remains straightforward.
// BetaOnboardingGate (gaka-93f.1.2): the single global inspector for the
// ?enable_beta_user_registration=true switch. Mounted in RootLayout — the one
// place that sees EVERY path, logged-in or not — it captures the URL flag
// (via useBetaRegistration) and, while the preview is active, redirects to
// /onboarding from anywhere so the new flow can be walked without logging out.
// Renders nothing; it only drives navigation.
function BetaOnboardingGate() {
  const { active } = useBetaRegistration();
  const location = useLocation();
  const navigate = useNavigate();

  useEffect(() => {
    if (active && !location.pathname.startsWith("/onboarding")) {
      navigate("/onboarding", { replace: true });
    }
  }, [active, location.pathname, navigate]);

  return null;
}

export function RootLayout() {
  return (
    <AuthProvider>
      <AnalyticsTracker />
      <BetaOnboardingGate />
      <Outlet />
    </AuthProvider>
  );
}

export function AppRoutes() {
  return (
    <Routes>
      <Route path="/" element={<RootRedirect />} />
      <Route path="/login" element={<Login />} />
      <Route path="/register" element={<Register />} />
      {/* Beta onboarding preview (gaka-93f.1.2). Reached via the
          BetaOnboardingGate when ?enable_beta_user_registration=true is set;
          renders the welcome -> demo -> signup flow. */}
      <Route path="/onboarding" element={<Onboarding />} />
      {/* Public profile — anonymous for visitors, editable for owners
          (gaka-ie3). EditableProfilePage handles the owner-check + mode
          toggle; non-owners get the read-only PublicDashboard render. */}
      <Route
        path="/p/:slug"
        element={
          <Suspense fallback={<PageFallback />}>
            <EditableProfilePage />
          </Suspense>
        }
      />
      <Route
        path="/app"
        element={
          <ProtectedRoute>
            <AppShell />
          </ProtectedRoute>
        }
      >
        {/* Every /app page composes <Page>, which owns its own vertical
            scroll region, so each renders directly into the no-scroll shell
            main (fe-pom-shell). */}
        <Route
          index
          element={
            <Suspense fallback={<PageFallback />}>
              <Overview />
            </Suspense>
          }
        />
        <Route
          path="projects"
          element={
            <Suspense fallback={<PageFallback />}>
              <Projects />
            </Suspense>
          }
        />
        {/* gaka-4ng: owner's profile inside the app skeleton. */}
        <Route
          path="profile"
          element={
            <Suspense fallback={<PageFallback />}>
              <InAppProfile />
            </Suspense>
          }
        />
        {/* gaka-gud: Goals as a top-level page (moved out of Settings). */}
        <Route
          path="goals"
          element={
            <Suspense fallback={<PageFallback />}>
              <Goals />
            </Suspense>
          }
        />
        <Route
          path="leaderboards"
          element={
            <Suspense fallback={<PageFallback />}>
              <Leaderboards />
            </Suspense>
          }
        />
        <Route
          path="heartbeats"
          element={
            <Suspense fallback={<PageFallback />}>
              <Heartbeats />
            </Suspense>
          }
        />
        <Route
          path="space/:id"
          element={
            <Suspense fallback={<PageFallback />}>
              <SpaceView />
            </Suspense>
          }
        />
        <Route
          path="import"
          element={
            <Suspense fallback={<PageFallback />}>
              <Import />
            </Suspense>
          }
        />
        {/* gaka-ebq: Logs lives under /app/admin/logs now (admin-only).
            Keep the old bookmarkable /app/logs URL working — the AdminRoute
            below decides whether the user actually gets to see it. */}
        <Route path="logs" element={<Navigate to="/app/admin/logs" replace />} />
        {/* Changelog still ships as a Settings tab. */}
        <Route
          path="changelog"
          element={<Navigate to="/app/settings?tab=changelog" replace />}
        />
        {/* /app/admin — admin-only section with three sub-tabs. Guarded
            twice: (1) the sidebar hides the entry entirely for non-admins,
            (2) AdminRoute redirects any direct URL hit. Each per-endpoint
            fetch also 403s on the server; this is UX, not security. */}
        <Route
          path="admin"
          element={
            <AdminRoute>
              <Suspense fallback={<PageFallback />}>
                <AdminPage />
              </Suspense>
            </AdminRoute>
          }
        >
          <Route index element={<Navigate to="/app/admin/labels" replace />} />
          <Route
            path="users"
            element={
              <Suspense fallback={<PageFallback />}>
                <UsersTab />
              </Suspense>
            }
          />
          <Route
            path="labels"
            element={
              <Suspense fallback={<PageFallback />}>
                <AdminTab />
              </Suspense>
            }
          />
          <Route
            path="backfill"
            element={
              <Suspense fallback={<PageFallback />}>
                <BackfillTab />
              </Suspense>
            }
          />
          <Route
            path="data"
            element={
              <Suspense fallback={<PageFallback />}>
                <DataTab />
              </Suspense>
            }
          />
          <Route
            path="logs"
            element={
              <Suspense fallback={<PageFallback />}>
                {/* embedded so the AdminPage's toolbar/tab-strip stays the
                    single page heading — Logs otherwise renders its own
                    PageToolbar title, which would double-print "Admin". */}
                <Logs embedded />
              </Suspense>
            }
          />
        </Route>
        <Route
          path="settings"
          element={
            <Suspense fallback={<PageFallback />}>
              <Settings />
            </Suspense>
          }
        />
        <Route
          path="wellness"
          element={
            <Suspense fallback={<PageFallback />}>
              <Wellness />
            </Suspense>
          }
        />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
