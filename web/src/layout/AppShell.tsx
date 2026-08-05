import { useState } from "react";
import { Outlet, useNavigate } from "react-router";
import { HeaderBar } from "@/layout/HeaderBar";
import { Sidebar } from "@/layout/Sidebar";
import { AppShellNoScroll } from "@/layout/AppShellNoScroll";
import { CreateSpaceDialog } from "@/features/spaces/CreateSpaceDialog";
import { WelcomeModal } from "@/features/onboarding/WelcomeModal";
import { useAuth } from "@/features/auth/useAuth";
import { useCollapsedSidebar } from "@/layout/useCollapsedSidebar";

// AppShell — the authed app frame. The layout is the no-scroll CSS-grid shell
// (AppShellNoScroll): the shell owns exactly one viewport (h-dvh, overflow
// hidden) so the sidebar and header stay pinned and never scroll away. All
// vertical scrolling happens INSIDE the content cell — either a page's own
// <Page.Content> (migrated pages) or the LegacyScrollLayout compat scroller
// in App.tsx (un-migrated pages). See docs/design/fe-pom-shell-spike.md.
export function AppShell() {
  const { username, logout } = useAuth();
  const navigate = useNavigate();
  const [createSpaceOpen, setCreateSpaceOpen] = useState(false);
  const { collapsed, toggleCollapsed } = useCollapsedSidebar();

  async function handleLogout() {
    await logout();
    navigate("/login");
  }

  return (
    <>
      <AppShellNoScroll
        sidebar={
          <Sidebar
            collapsed={collapsed}
            onToggleCollapsed={toggleCollapsed}
            onLogout={handleLogout}
            onCreateSpace={() => setCreateSpaceOpen(true)}
          />
        }
        header={<HeaderBar username={username} onLogout={handleLogout} />}
      >
        <Outlet />
      </AppShellNoScroll>

      <CreateSpaceDialog
        open={createSpaceOpen}
        onOpenChange={setCreateSpaceOpen}
      />
      <WelcomeModal />
    </>
  );
}
