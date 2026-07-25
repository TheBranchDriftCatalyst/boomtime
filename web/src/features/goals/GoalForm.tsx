// GoalForm — placeholder for the create/edit modal.
//
// The real form + recursive PredicateBuilder land in stage 6 of
// gaka-wpb. This stub lets the Goals tab register at stage 5 and
// render the empty state / list without a broken import.
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dialog";
import type { Goal } from "@/types/api";

export function GoalForm({
  open,
  onOpenChange,
  editing,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  editing: Goal | null;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{editing ? `Edit "${editing.name}"` : "New goal"}</DialogTitle>
          <DialogDescription>
            The predicate builder ships in the next stage — this dialog is
            a placeholder while the goals list + tile widgets go up.
          </DialogDescription>
        </DialogHeader>
      </DialogContent>
    </Dialog>
  );
}
