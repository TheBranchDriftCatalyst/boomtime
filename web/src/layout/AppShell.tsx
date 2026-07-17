import { useState } from "react";
import { Outlet, useNavigate } from "react-router";
import { HeaderBar } from "@/layout/HeaderBar";
import { Sidebar } from "@/layout/Sidebar";
import { CreateSpaceDialog } from "@/features/spaces/CreateSpaceDialog";
import { WelcomeModal } from "@/features/onboarding/WelcomeModal";
import { useAuth } from "@/features/auth/useAuth";
import { useCollapsedSidebar } from "@/layout/useCollapsedSidebar";

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
    <div className="flex h-full min-h-screen bg-background">
      <Sidebar
        collapsed={collapsed}
        onToggleCollapsed={toggleCollapsed}
        onLogout={handleLogout}
        onCreateSpace={() => setCreateSpaceOpen(true)}
      />

      {/* Main */}
      <div className="flex flex-1 flex-col overflow-hidden">
        <HeaderBar username={username} onLogout={handleLogout} />

        <main className="flex-1 overflow-y-auto p-6">
          <Outlet />
        </main>
      </div>

      <CreateSpaceDialog
        open={createSpaceOpen}
        onOpenChange={setCreateSpaceOpen}
      />
      <WelcomeModal />
    </div>
  );
}
