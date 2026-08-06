// DashboardEditChrome (gaka-lzr, Phase 4) — the Page.Header chrome for the
// in-app dashboard editor: an Edit | Preview segmented toggle, Undo / Redo
// buttons, a subtle dirty indicator, and the ⌘Z / ⌘⇧Z keyboard shortcuts.
//
// Placed inline in the toolbar (unlike the profile page's fixed floating pill)
// so it flows with the existing range-picker / widgets-panel controls.
import { useEffect } from "react";
import { Eye, Pencil, Redo2, Undo2 } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { cn } from "@/lib/utils";

export type DashboardEditMode = "preview" | "edit";

export interface DashboardEditChromeProps {
  mode: DashboardEditMode;
  onModeChange: (m: DashboardEditMode) => void;
  canUndo: boolean;
  canRedo: boolean;
  onUndo: () => void;
  onRedo: () => void;
  isDirty: boolean;
}

export function DashboardEditChrome({
  mode,
  onModeChange,
  canUndo,
  canRedo,
  onUndo,
  onRedo,
  isDirty,
}: DashboardEditChromeProps) {
  const isEdit = mode === "edit";

  // ⌘Z / Ctrl+Z → undo; ⌘⇧Z / Ctrl+Shift+Z → redo. Only while editing, and
  // never when the user is typing into a field.
  useEffect(() => {
    if (!isEdit) return;
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey)) return;
      if (e.key.toLowerCase() !== "z") return;
      const target = e.target as HTMLElement | null;
      const tag = target?.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA" || target?.isContentEditable) {
        return;
      }
      e.preventDefault();
      if (e.shiftKey) {
        if (canRedo) onRedo();
      } else if (canUndo) {
        onUndo();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [isEdit, canUndo, canRedo, onUndo, onRedo]);

  return (
    <div className="flex items-center gap-2" data-testid="dashboard-edit-chrome">
      {isEdit && (
        <>
          <span
            className="font-mono text-[10px] uppercase tracking-[0.15em] text-muted-foreground"
            data-testid="dashboard-edit-dirty"
            data-dirty={isDirty || undefined}
          >
            {isDirty ? "unsaved" : "saved"}
          </span>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={onUndo}
            disabled={!canUndo}
            aria-label="Undo"
            title="Undo (⌘Z)"
            data-testid="dashboard-edit-undo"
          >
            <Undo2 size={14} />
          </Button>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={onRedo}
            disabled={!canRedo}
            aria-label="Redo"
            title="Redo (⌘⇧Z)"
            data-testid="dashboard-edit-redo"
          >
            <Redo2 size={14} />
          </Button>
        </>
      )}
      <div
        className="flex items-center gap-0 rounded-full border border-border bg-background/90 p-0.5"
        role="group"
        aria-label="Dashboard view mode"
        data-testid="dashboard-mode-toggle"
      >
        <ModeButton
          active={isEdit}
          onClick={() => onModeChange("edit")}
          icon={<Pencil size={12} />}
          label="Edit"
          testId="dashboard-mode-edit"
        />
        <ModeButton
          active={!isEdit}
          onClick={() => onModeChange("preview")}
          icon={<Eye size={12} />}
          label="Preview"
          testId="dashboard-mode-preview"
        />
      </div>
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
