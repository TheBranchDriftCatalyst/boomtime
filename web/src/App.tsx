import { Navigate, Route, Routes } from "react-router";
import { AppShell } from "@/components/AppShell";
import { ProtectedRoute } from "@/components/ProtectedRoute";
import { useAuth } from "@/hooks/useAuth";
import { Spinner } from "@/components/Spinner";
import { Heartbeats } from "@/pages/Heartbeats";
import { Import } from "@/pages/Import";
import { Leaderboards } from "@/pages/Leaderboards";
import { Login } from "@/pages/Login";
import { Overview } from "@/pages/Overview";
import { Projects } from "@/pages/Projects";
import { Register } from "@/pages/Register";
import { Settings } from "@/pages/Settings";
import { SpaceView } from "@/pages/SpaceView";

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
        <Route path="settings" element={<Settings />} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}
