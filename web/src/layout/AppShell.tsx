import { useState } from "react";
import { Outlet, useNavigate } from "react-router";
import { toast } from "sonner";
import { HeaderBar } from "@/layout/HeaderBar";
import { Sidebar } from "@/layout/Sidebar";
import { CreateSpaceDialog } from "@/features/spaces/CreateSpaceDialog";
import { CreateTokenModal } from "@/features/tokens/CreateTokenModal";
import { TokenListModal } from "@/features/tokens/TokenListModal";
import { WelcomeModal } from "@/features/onboarding/WelcomeModal";
import { useAuth } from "@/features/auth/useAuth";
import { useCollapsedSidebar } from "@/layout/useCollapsedSidebar";
import { api } from "@/lib/api";

export function AppShell() {
  const { username, logout } = useAuth();
  const navigate = useNavigate();
  const [newToken, setNewToken] = useState<string | null>(null);
  const [tokensOpen, setTokensOpen] = useState(false);
  const [createSpaceOpen, setCreateSpaceOpen] = useState(false);
  const { collapsed, toggleCollapsed } = useCollapsedSidebar();

  async function createToken() {
    try {
      const res = await api.createApiToken();
      setNewToken(res.apiToken);
    } catch {
      toast.error("Failed to create API token");
    }
  }

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
        <HeaderBar
          username={username}
          onCreateToken={createToken}
          onOpenTokens={() => setTokensOpen(true)}
          onLogout={handleLogout}
        />

        <main className="flex-1 overflow-y-auto p-6">
          <Outlet />
        </main>
      </div>

      <CreateTokenModal token={newToken} onClose={() => setNewToken(null)} />
      <TokenListModal open={tokensOpen} onClose={() => setTokensOpen(false)} />
      <CreateSpaceDialog
        open={createSpaceOpen}
        onOpenChange={setCreateSpaceOpen}
      />
      <WelcomeModal />
    </div>
  );
}
