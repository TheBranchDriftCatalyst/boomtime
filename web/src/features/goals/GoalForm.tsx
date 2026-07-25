// GoalForm — the create/edit modal for a goal (gaka-wpb).
//
// Two-way binding pattern:
//   - Local state = { name, description, spec } during the modal's
//     lifetime. Initialized from `editing` when opening for edit;
//     reset to a default (single time leaf) on create.
//   - Submit invokes the create OR update mutation depending on
//     mode; success closes the modal.
//   - Server-side validation errors (400 with a message) surface as
//     an inline banner; the modal STAYS OPEN so the author can fix
//     and retry without losing the tree.
//
// The recursive predicate builder is PredicateBuilder — see its
// doc-comment for the state-management design rationale.
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dialog";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Label } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";
import {
  PredicateBuilder,
  defaultLeaf,
} from "@/features/goals/PredicateBuilder";
import { useGoalMutations } from "@/features/goals/useGoals";
import { ApiError } from "@/lib/api";
import type { Goal, Predicate } from "@/types/api";

interface GoalFormProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  // Non-null → edit mode; null → create mode.
  editing: Goal | null;
}

export function GoalForm({ open, onOpenChange, editing }: GoalFormProps) {
  const { create, update } = useGoalMutations();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [spec, setSpec] = useState<Predicate>(defaultLeaf());
  const [error, setError] = useState<string | null>(null);

  // Reset state whenever the modal (re-)opens. Prefill from `editing`
  // in edit mode; reset to a fresh leaf otherwise. We intentionally
  // do NOT depend on `editing` alone — a re-open with the same goal
  // should also reset (in case the user was mid-edit and cancelled).
  useEffect(() => {
    if (!open) return;
    if (editing) {
      setName(editing.name);
      setDescription(editing.description ?? "");
      setSpec(structuredClone(editing.spec));
    } else {
      setName("");
      setDescription("");
      setSpec(defaultLeaf());
    }
    setError(null);
  }, [open, editing]);

  function onSubmit() {
    setError(null);
    const trimmed = name.trim();
    if (!trimmed) {
      setError("Name is required");
      return;
    }
    const body = {
      name: trimmed,
      description: description.trim() || undefined,
      spec,
    };
    const onError = (err: unknown) => {
      // Server-side validation errors come back as ApiError with the
      // reason in `.message`. Show inline; keep modal open.
      const msg =
        err instanceof ApiError ? err.message : "Failed to save goal";
      setError(msg);
    };
    const onSuccess = (verb: "created" | "updated") => {
      toast.success(`Goal ${verb}`);
      onOpenChange(false);
    };

    if (editing) {
      update.mutate(
        { id: editing.id, body },
        { onSuccess: () => onSuccess("updated"), onError },
      );
    } else {
      create.mutate(body, {
        onSuccess: () => onSuccess("created"),
        onError,
      });
    }
  }

  const pending = create.isPending || update.isPending;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl max-h-[85vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>
            {editing ? `Edit "${editing.name}"` : "New goal"}
          </DialogTitle>
          <DialogDescription>
            A goal is a predicate tree over your activity. The simplest
            shape is a single time-on-axis target; use "Change type" on
            any node to add AND / OR / NOT compositions or a streak.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="grid gap-3 md:grid-cols-2">
            <div>
              <Label htmlFor="goal-name">Name</Label>
              <Input
                id="goal-name"
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="e.g. weekly-go"
              />
            </div>
            <div>
              <Label htmlFor="goal-desc">Description (optional)</Label>
              <Input
                id="goal-desc"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder="What this goal is for"
              />
            </div>
          </div>

          <div>
            <Label>Predicate</Label>
            <div className="mt-1">
              <PredicateBuilder node={spec} onChange={setSpec} />
            </div>
          </div>

          {error && (
            <div className="rounded-md border border-destructive/40 bg-destructive/10 p-2 text-sm text-destructive">
              {error}
            </div>
          )}
        </div>

        <DialogFooter>
          <Button
            variant="ghost"
            onClick={() => onOpenChange(false)}
            disabled={pending}
          >
            Cancel
          </Button>
          <Button onClick={onSubmit} disabled={pending}>
            {editing ? "Save changes" : "Create goal"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
