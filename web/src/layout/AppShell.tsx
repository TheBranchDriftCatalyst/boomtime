import { useState } from "react";
import { Outlet, useNavigate } from "react-router";
import { HeaderBar } from "@/layout/HeaderBar";
import { Sidebar } from "@/layout/Sidebar";
import { AppShellNoScroll } from "@/layout/AppShellNoScroll";
import { HeaderSlotProvider } from "@/layout/HeaderSlot";
import { CreateSpaceDialog } from "@/features/spaces/CreateSpaceDialog";
import { WelcomeModal } from "@/features/onboarding/WelcomeModal";
import { CommandPalette } from "@/components/CommandPalette";
import { KeyboardShortcuts } from "@/components/KeyboardShortcuts";
import { useAuth } from "@/features/auth/useAuth";
import { useCollapsedSidebar } from "@/layout/useCollapsedSidebar";
import { useJobNotifications } from "@/features/jobs/useJobNotifications";
import { NotificationsProvider } from "@/features/notify/NotificationsProvider";

// AppShell — the authed app frame. The layout is the no-scroll CSS-grid shell
// (AppShellNoScroll): the shell owns exactly one viewport (h-dvh, overflow
// hidden) so the sidebar and header stay pinned and never scroll away. All
// vertical scrolling happens INSIDE the content cell — every /app page composes
// <Page>, whose <Page.Content> is the sole scroller. See
// docs/design/fe-pom-shell-spike.md.
export function AppShell() {
  const { username, logout } = useAuth();
  const navigate = useNavigate();
  const [createSpaceOpen, setCreateSpaceOpen] = useState(false);
  const { collapsed, toggleCollapsed } = useCollapsedSidebar();
  // Push toasts when one of the caller's jobs completes/fails (gaka-hney.6).
  useJobNotifications();

  async function handleLogout() {
    await logout();
    navigate("/login");
  }

  return (
    // NotificationsProvider owns the notify WS + durable-notification store; it
    // wraps the shell so the header bell (and any page) can read it.
    <NotificationsProvider>
      {/* HeaderSlotProvider must wrap BOTH the header and the routed <Outlet/>
          so a page (settings/admin) can hoist its tab strip up into HeaderBar
          to reclaim the page's title+tab row. See @/layout/HeaderSlot. */}
      <HeaderSlotProvider>
        <AppShellNoScroll
          sidebar={
            <Sidebar
              collapsed={collapsed}
              onToggleCollapsed={toggleCollapsed}
              onLogout={handleLogout}
              onCreateSpace={() => setCreateSpaceOpen(true)}
            />
          }
          header={
            <HeaderBar
              username={username}
              onLogout={handleLogout}
              onCreateSpace={() => setCreateSpaceOpen(true)}
            />
          }
        >
          <Outlet />
        </AppShellNoScroll>
      </HeaderSlotProvider>

      <CreateSpaceDialog
        open={createSpaceOpen}
        onOpenChange={setCreateSpaceOpen}
      />
      <CommandPalette
        onCreateSpace={() => setCreateSpaceOpen(true)}
        onLogout={handleLogout}
      />
      <KeyboardShortcuts />
      <WelcomeModal />
    </NotificationsProvider>
  );
}
