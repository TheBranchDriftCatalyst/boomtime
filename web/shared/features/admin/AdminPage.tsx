// AdminPage — the /app/admin section shell.
//
// boom-zp2s: reduced to a thin re-export of the shared AdminSectionPage, which
// composes its tab strip from the admin-registration seam (getAdminGroups) and
// renders the domain-grouped strip + an <Outlet/> for the routed tab body. The
// tabs + their grouping (Operations / CatalystBooks / Boomtime) are declared by
// each domain's register module — this file owns no tab list anymore. Kept as a
// stable import path for the App.tsx route.
export { AdminSectionPage as AdminPage } from "@shared/shared/admin/AdminSectionPage";
