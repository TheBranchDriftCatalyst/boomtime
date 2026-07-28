import { lazy, Suspense } from "react";
import { Navigate, Route, Routes } from "react-router";
import { AppShell } from "@/layout/AppShell";
import { ProtectedRoute } from "@/app/ProtectedRoute";
import { AdminRoute } from "@/app/AdminRoute";
import { useAuth } from "@/features/auth/useAuth";
import { Spinner } from "@/components/Spinner";
// Auth pages are eagerly imported: the pre-auth bundle is tiny and Login is
// the most-common landing page after a fresh visit — code-splitting it just
// costs a network round-trip on the critical path.
import { Login } from "@/features/auth/Login";
import { Register } from "@/features/auth/Register";

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
const Logs = lazy(() =>
  import("@/features/logs/Logs").then((m) => ({ default: m.Logs })),
);
// Public profile lives OUTSIDE the /app tree — /p/:slug is unauthenticated
// and renders its own minimal shell (no sidebar, no header).
const PublicDashboard = lazy(() =>
  import("@/features/publicprofile/PublicDashboard").then((m) => ({
    default: m.PublicDashboard,
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

export function App() {
  return (
    <Routes>
      <Route path="/" element={<RootRedirect />} />
      <Route path="/login" element={<Login />} />
      <Route path="/register" element={<Register />} />
      {/* Public profile — unauthenticated, no shell. */}
      <Route
        path="/p/:slug"
        element={
          <Suspense fallback={<PageFallback />}>
            <PublicDashboard />
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
