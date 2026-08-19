// AdminRoute — route-level guard for /app/admin/* (gaka-ebq).
//
// The per-endpoint admin gate on the server is the actual security
// boundary; this component only exists so a non-admin who follows a
// deep link doesn't stare at an empty tab strip and a wall of 403s
// scrolling in the network panel. Redirects to /app (Overview) instead
// of rendering a 403 page — the URL is a leak of the section's name
// but the sidebar hides the entry too, so it's not worth a special
// "unauthorized" page for what's a mistyped URL 99% of the time.
//
// While the is_admin bit is loading we render a Spinner rather than
// letting the child mount + fetch + 403 for a beat.
import type { ReactNode } from "react";
import { Navigate } from "react-router";
import { Spinner } from "@thebranchdriftcatalyst/catalyst-ui/ui/spinner";
import { useIsAdmin } from "@shared/features/auth/useIsAdmin";

export function AdminRoute({ children }: { children: ReactNode }) {
  const { isAdmin, isLoading } = useIsAdmin();

  if (isLoading) {
    return (
      <div className="flex h-[60vh] items-center justify-center">
        <Spinner />
      </div>
    );
  }

  if (!isAdmin) return <Navigate to="/app" replace />;
  return <>{children}</>;
}
