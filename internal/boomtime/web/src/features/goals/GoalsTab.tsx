// GoalsTab — the Settings > Goals tab body (gaka-wpb).
//
// Renders the "New goal" button (opens the create form) + the list
// of existing goals. The GoalForm modal (which houses the recursive
// PredicateBuilder — the one novel piece per the plan) is mounted
// here so the same form serves both create and edit flows; parent
// state tracks {mode, editing?} for the modal.
//
// Delete uses a confirm() prompt for now — a full destructive dialog
// (mirroring DestructiveActionDialog) is not needed because deleting
// a goal is fully reversible (a fresh POST recreates it); the confirm
// exists only to catch accidental clicks. If we want a nicer UX later
// we can promote to a shadcn Dialog without breaking the surface.
import { useState } from "react";
import { HelpCircle, Plus } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/popover";
import { GoalsListSkeleton } from "@/components/Skeletons";
import { GoalForm } from "@boomtime/features/goals/GoalForm";
import { GoalNearnessStrip } from "@boomtime/features/goals/GoalNearnessStrip";
import { GoalsList } from "@boomtime/features/goals/GoalsList";
import { useGoalMutations, useGoalsQuery } from "@boomtime/features/goals/useGoals";
import type { Goal } from "@/types/api";

export function GoalsTab() {
  const { data: goals, isLoading } = useGoalsQuery();
  const { remove } = useGoalMutations();
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<Goal | null>(null);

  function openCreate() {
    setEditing(null);
    setFormOpen(true);
  }
  function openEdit(g: Goal) {
    setEditing(g);
    setFormOpen(true);
  }
  function onDelete(g: Goal) {
    if (!window.confirm(`Delete goal "${g.name}"?`)) return;
    remove.mutate(g.id, {
      onSuccess: () => toast.success(`Deleted goal "${g.name}"`),
      onError: () => toast.error("Failed to delete goal"),
    });
  }

  if (isLoading) return <GoalsListSkeleton rows={4} />;

  return (
    <div className="space-y-5">
      <div className="flex items-start justify-between gap-4">
        <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
          <span>
            Declare what you want to do — the app tracks your progress.{" "}
            <span className="text-foreground/70">Private by default.</span>
          </span>
          <Popover>
            <PopoverTrigger asChild>
              <button
                type="button"
                aria-label="How goals work"
                className="shrink-0 rounded-full p-0.5 text-muted-foreground/70 transition-colors hover:bg-secondary hover:text-foreground"
              >
                <HelpCircle className="h-3.5 w-3.5" />
              </button>
            </PopoverTrigger>
            <PopoverContent align="start" className="w-[21rem] space-y-3 p-4">
              <div className="space-y-1">
                <p className="text-xs font-semibold uppercase tracking-wide text-foreground">
                  Authoring
                </p>
                <p className="text-xs leading-relaxed text-muted-foreground">
                  Set a simple time target ("1 hour a week on Go") or compose an
                  expression tree with{" "}
                  <span className="font-medium text-foreground">AND / OR / NOT</span>{" "}
                  over time-on-axis, streak, and active-days predicates.
                </p>
              </div>
              <div className="space-y-1">
                <p className="text-xs font-semibold uppercase tracking-wide text-foreground">
                  On your dashboard
                </p>
                <p className="text-xs leading-relaxed text-muted-foreground">
                  Add a{" "}
                  <span className="font-medium text-foreground">Goal Ring</span>,{" "}
                  <span className="font-medium text-foreground">Goal Progress</span>,
                  or{" "}
                  <span className="font-medium text-foreground">Goal List</span>{" "}
                  widget — or embed one on your README / site — via{" "}
                  <span className="font-medium text-foreground">Settings › Widgets</span>.
                </p>
              </div>
              <div className="space-y-1">
                <p className="text-xs font-semibold uppercase tracking-wide text-foreground">
                  Visibility
                </p>
                <p className="text-xs leading-relaxed text-muted-foreground">
                  Goals stay private until you flip a goal's{" "}
                  <span className="font-medium text-foreground">Public</span>{" "}
                  toggle in the New / Edit form — then its name + progress can
                  appear on those widgets.
                </p>
              </div>
            </PopoverContent>
          </Popover>
        </p>
        <Button onClick={openCreate} size="sm" className="shrink-0">
          <Plus className="mr-1 h-4 w-4" />
          New goal
        </Button>
      </div>

      <GoalNearnessStrip goals={goals ?? []} />

      <GoalsList
        goals={goals ?? []}
        onEdit={openEdit}
        onRemove={onDelete}
        onCreate={openCreate}
      />

      <GoalForm
        open={formOpen}
        onOpenChange={setFormOpen}
        editing={editing}
      />
    </div>
  );
}
