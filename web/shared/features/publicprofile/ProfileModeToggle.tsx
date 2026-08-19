// ProfileModeToggle — floating pill switch (Edit | Preview) rendered above
// the public profile shell for the owner only (gaka-ie3).
//
// Positioning: fixed top-right so it stays reachable even when the user
// scrolls a long profile. z-index sits above the grid tiles but below
// modal overlays.
import { Eye, Pencil } from "lucide-react";
import { cn } from "@shared/lib/utils";

export type ProfileMode = "preview" | "edit";

export interface ProfileModeToggleProps {
  mode: ProfileMode;
  onChange: (m: ProfileMode) => void;
}

export function ProfileModeToggle({ mode, onChange }: ProfileModeToggleProps) {
  return (
    <div
      className="fixed right-4 top-4 z-40 flex items-center gap-0 rounded-full border border-border bg-background/90 p-0.5 shadow-lg backdrop-blur"
      role="group"
      aria-label="Profile view mode"
      data-testid="profile-mode-toggle"
    >
      <ModeButton
        active={mode === "edit"}
        onClick={() => onChange("edit")}
        icon={<Pencil size={12} />}
        label="Edit"
        testId="profile-mode-edit"
      />
      <ModeButton
        active={mode === "preview"}
        onClick={() => onChange("preview")}
        icon={<Eye size={12} />}
        label="Preview"
        testId="profile-mode-preview"
      />
    </div>
  );
}

interface ModeButtonProps {
  active: boolean;
  onClick: () => void;
  icon: React.ReactNode;
  label: string;
  testId: string;
}

function ModeButton({ active, onClick, icon, label, testId }: ModeButtonProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      data-testid={testId}
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full px-3 py-1 text-xs font-medium transition-colors",
        active
          ? "bg-primary text-primary-foreground"
          : "text-muted-foreground hover:text-foreground",
      )}
    >
      {icon}
      {label}
    </button>
  );
}
