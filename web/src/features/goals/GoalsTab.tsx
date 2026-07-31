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
import { Plus } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Spinner } from "@thebranchdriftcatalyst/catalyst-ui/ui/spinner";
import { GoalForm } from "@/features/goals/GoalForm";
import { GoalsList } from "@/features/goals/GoalsList";
import { useGoalMutations, useGoalsQuery } from "@/features/goals/useGoals";
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

  if (isLoading) return <Spinner />;

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <p className="max-w-2xl text-sm text-muted-foreground">
          Goals let you declare what you WANT to do — a target the app
          measures your progress against. Author a simple time target
          ("1 hour a week on Go") or compose an expression tree with
          AND / OR / NOT over time-on-axis, streak, and active-days
          predicates. Add a{" "}
          <span className="font-medium text-foreground">Goal Ring</span>,{" "}
          <span className="font-medium text-foreground">Goal Progress</span>,
          or <span className="font-medium text-foreground">Goal List</span>{" "}
          widget to your public dashboard to render progress publicly.
        </p>
        <Button onClick={openCreate} size="sm">
          <Plus className="mr-1 h-4 w-4" />
          New goal
        </Button>
      </div>

      <GoalsList
        goals={goals ?? []}
        onEdit={openEdit}
        onRemove={onDelete}
      />

      <GoalForm
        open={formOpen}
        onOpenChange={setFormOpen}
        editing={editing}
      />
    </div>
  );
}
