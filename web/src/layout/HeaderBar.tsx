import { useNavigate } from "react-router";
import { Download, KeyRound, LogOut, User } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { ThemeToggle } from "@/layout/ThemeToggle";

interface HeaderBarProps {
  username: string;
  onCreateToken: () => void;
  onOpenTokens: () => void;
  onLogout: () => void;
}

/** Top header: theme toggle, new-token button, and the user menu. */
export function HeaderBar({
  username,
  onCreateToken,
  onOpenTokens,
  onLogout,
}: HeaderBarProps) {
  const navigate = useNavigate();

  return (
    <header className="flex h-16 items-center justify-end gap-3 border-b bg-card px-6">
      <ThemeToggle />
      <Button
        variant="outline"
        size="sm"
        onClick={onCreateToken}
        title="Create a new API token"
      >
        <KeyRound className="h-4 w-4" />
        New API token
      </Button>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <button className="flex items-center gap-2">
            <span className="hidden text-sm text-muted-foreground sm:inline">
              {username}
            </span>
            <div className="flex h-9 w-9 items-center justify-center rounded-full bg-secondary text-sm font-semibold uppercase">
              {username ? username.charAt(0) : <User className="h-4 w-4" />}
            </div>
          </button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-48">
          <DropdownMenuLabel>{username}</DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={onOpenTokens}>
            <KeyRound className="h-4 w-4" />
            Tokens
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => navigate("/app/import")}>
            <Download className="h-4 w-4" />
            Import data
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={onLogout}>
            <LogOut className="h-4 w-4" />
            Logout
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </header>
  );
}
