import { lazy, Suspense } from "react";
import { Navigate, Route, Routes } from "react-router";
import { AppShell } from "@/layout/AppShell";
import { ProtectedRoute } from "@/app/ProtectedRoute";
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
        {/* Logs + Changelog moved into Settings tabs; keep old URLs working. */}
        <Route
          path="logs"
          element={<Navigate to="/app/settings?tab=logs" replace />}
        />
        <Route
          path="changelog"
          element={<Navigate to="/app/settings?tab=changelog" replace />}
        />
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
