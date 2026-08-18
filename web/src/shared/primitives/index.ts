// Shared FE primitives — the curated, DOMAIN-FREE surface both domains build on.
//
// This barrel re-exports ONLY portable, domain-agnostic primitives (page POM,
// tab-nav, header-slot bridge, admin shells). It deliberately re-exports NO
// feature/domain code, so it can't drag a domain into a build and is safe for
// tree-shaking. The layout/* primitives are already domain-free ("can graduate
// to catalyst-ui unchanged"); this barrel is their stable import home ahead of
// the physical move to `web/shared/`.

// Page object model — the sole scroll region every /app page composes.
export { Page } from "@/layout/Page";
export type {
  PageProps,
  PageHeaderProps,
  PageBodyProps,
  PageContentProps,
  PageAsideProps,
} from "@/layout/Page";

// Tab navigation — flat + domain-grouped variants and the per-tab class helper.
export {
  TabNav,
  GroupedTabNav,
  tabClass,
  PageTabStrip,
  pageTabClass,
} from "@/layout/PageTabs";
export type {
  TabNavProps,
  TabNavVariant,
  GroupedTabNavProps,
  TabNavGroup,
} from "@/layout/PageTabs";

// Header slot — the portal-in-state that lets a page hoist chrome into the bar.
export {
  HeaderSlotProvider,
  useHeaderSlot,
  useHeaderSlotNode,
} from "@/layout/HeaderSlot";

// Admin shells — the section page (tab strip + Outlet) + the per-tab base.
export { AdminSectionPage } from "@/shared/admin/AdminSectionPage";
export { AdminTabShell, AdminAccessCard } from "@/shared/admin/AdminTabShell";
export type { AdminTabShellProps } from "@/shared/admin/AdminTabShell";
