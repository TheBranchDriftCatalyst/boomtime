import type { ReactNode } from "react";
import { Navigate } from "react-router";
import { Spinner } from "@/components/Spinner";
import { useAuth } from "@/features/auth/useAuth";

export function ProtectedRoute({ children }: { children: ReactNode }) {
  const { isLoggedIn, bootstrapping } = useAuth();

  if (bootstrapping) {
    return (
      <div className="flex h-screen items-center justify-center">
        <Spinner />
      </div>
    );
  }

  if (!isLoggedIn) return <Navigate to="/login" replace />;
  return <>{children}</>;
}
