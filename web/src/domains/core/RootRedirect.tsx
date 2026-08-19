// The "/" landing decision, extracted so the core register module can lazy()
// it — keeping registration side-effect free (no auth/hook module is evaluated
// until this route actually renders, mirroring the lazy tab bodies).
//
// Bounces to /app when logged in, /login otherwise; shows a full-screen Spinner
// while auth bootstraps. Rendered as a route element, so it sees the
// AuthProvider mounted by RootLayout.
import { Navigate } from "react-router";
import { Spinner } from "@thebranchdriftcatalyst/catalyst-ui/ui/spinner";

import { useAuth } from "@/features/auth/useAuth";

export function RootRedirect() {
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
