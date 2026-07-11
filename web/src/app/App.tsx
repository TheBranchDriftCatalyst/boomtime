import { Navigate, Route, Routes } from "react-router";
import { AppShell } from "@/layout/AppShell";
import { ProtectedRoute } from "@/app/ProtectedRoute";
import { useAuth } from "@/features/auth/useAuth";
import { Spinner } from "@/components/Spinner";
import { Heartbeats } from "@/features/heartbeats/Heartbeats";
import { Import } from "@/features/import/Import";
import { Leaderboards } from "@/features/leaderboards/Leaderboards";
import { Logs } from "@/features/logs/Logs";
import { Login } from "@/features/auth/Login";
import { Overview } from "@/features/overview/Overview";
import { Projects } from "@/features/projects/Projects";
import { Register } from "@/features/auth/Register";
import { Settings } from "@/features/curation/Settings";
import { SpaceView } from "@/features/spaces/SpaceView";

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

export function App() {
  return (
    <Routes>
      <Route path="/" element={<RootRedirect />} />
      <Route path="/login" element={<Login />} />
      <Route path="/register" element={<Register />} />
      <Route
        path="/app"
        element={
          <ProtectedRoute>
            <AppShell />
          </ProtectedRoute>
        }
      >
        <Route index element={<Overview />} />
        <Route path="projects" element={<Projects />} />
        <Route path="leaderboards" element={<Leaderboards />} />
        <Route path="heartbeats" element={<Heartbeats />} />
        <Route path="space/:id" element={<SpaceView />} />
        <Route path="import" element={<Import />} />
        <Route path="logs" element={<Logs />} />
        <Route path="settings" element={<Settings />} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
