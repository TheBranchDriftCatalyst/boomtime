import { useEffect, type ReactElement } from "react";
import { Outlet, Route, Routes, useLocation, useNavigate } from "react-router";
import { AnalyticsTracker } from "@shared/app/AnalyticsTracker";
import { AuthProvider } from "@shared/features/auth/useAuth";
import { useBetaRegistration } from "@shared/features/onboarding/betaRegistration";
import { getRoutes } from "@shared/shared/routing/registry";
import type { RouteDef } from "@shared/shared/routing/types";

// gaka-zp2s: the route table is now REGISTRATION-DRIVEN, mirroring the nav /
// settings / admin seams. App.tsx (the shell) imports NO domain page — every
// route is pushed into @shared/shared/routing/registry by a domain's register module
// (core owns the shell + auth/public routes + Overview/Settings/Admin;
// boomtime owns the analytics pages; books owns the library + books admin tab).
// The host entry composes all three (see @shared/app/registerDomains); a standalone
// books entry composes only core + books, so the boomtime page modules never
// enter the module graph and tree-shake away — the router included.
//
// This build step reproduces the exact same nested `<Routes>` tree the static
// list used to declare: top-level routes are siblings of the "/app" shell
// wrapper; routes whose `parent` matches a wrapper's `id` nest inside it
// (recursively, so /app/admin's tabs nest under the admin sub-shell). Ordering
// is cosmetic — react-router ranks by path specificity — so behavior is
// byte-identical regardless of registration order.
function buildRouteElements(
  defs: RouteDef[],
  parent?: string,
): ReactElement[] {
  return defs
    .filter((d) => (d.parent ?? undefined) === parent)
    .map((d) => {
      // Only recurse for routes that declare an `id` (mount points). A route
      // with no `id` is a leaf: recursing with `d.id === undefined` would
      // re-match the root bucket (parent === undefined) and infinite-loop.
      const children = d.id ? buildRouteElements(defs, d.id) : [];
      if (d.index) {
        return (
          <Route key={`${parent ?? "root"}:index`} index element={d.element} />
        );
      }
      const key = d.id ?? `${parent ?? "root"}:${d.path}`;
      return (
        <Route key={key} path={d.path} element={d.element}>
          {children.length ? children : undefined}
        </Route>
      );
    });
}

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

// gaka-ie3: split into two exports for the data-router migration.
// `RootLayout` is the top-level route element mounted by createBrowserRouter —
// it owns providers that historically lived in main.tsx (AuthProvider +
// AnalyticsTracker) but need access to the router context. `AppRoutes` is the
// leaf that renders the classic nested <Routes> tree — now built from the route
// registry (see buildRouteElements above).
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
  return <Routes>{buildRouteElements(getRoutes())}</Routes>;
}
