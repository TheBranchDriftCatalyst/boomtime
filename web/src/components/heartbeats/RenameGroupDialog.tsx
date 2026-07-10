import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Combobox } from "@/components/ui/combobox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { useAxisValues } from "@/hooks/useAxisValues";
import { useCurationMutations } from "@/hooks/useCuration";
import { axisLabel } from "@/components/heartbeats/axes";
import type { HeartbeatAxis } from "@/types/api";

interface RenameGroupDialogProps {
  open: boolean;
  axis: HeartbeatAxis;
  value: string;
  onClose: () => void;
}

export function RenameGroupDialog({
  open,
  axis,
  value,
  onClose,
}: RenameGroupDialogProps) {
  const { add } = useCurationMutations();
  const [newValue, setNewValue] = useState(value);

  // Existing values of the same axis, so a merge is one click. Only fetched
  // while the dialog is open. Exclude the current value (renaming to itself is
  // a no-op).
  const { options, isLoading } = useAxisValues(axis, open);
  const mergeOptions = useMemo(
    () => options.filter((o) => o.value !== value),
    [options, value],
  );

  // Reset the field whenever a different group opens the dialog.
  useEffect(() => {
    if (open) setNewValue(value);
  }, [open, value]);

  function submit(e: React.FormEvent) {
    e.preventDefault();
    const trimmed = newValue.trim();
    if (!trimmed || trimmed === value) {
      onClose();
      return;
    }
    add.mutate(
      { axis, action: "rename", matchValue: value, newValue: trimmed },
      {
        onSuccess: () => {
          toast.success(`Renamed ${axisLabel(axis)} "${value}" → "${trimmed}"`);
          onClose();
        },
        onError: () => toast.error("Failed to rename"),
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Rename {axisLabel(axis).toLowerCase()}</DialogTitle>
          <DialogDescription>
            Renames every heartbeat with this value (merges if the name already
            exists) and applies to future imports too.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <div className="space-y-1.5">
            <Label>
              Rename all {axisLabel(axis)}{" "}
              <span className="font-mono">&quot;{value}&quot;</span> →
            </Label>
            <Combobox
              options={mergeOptions}
              value={newValue}
              onSelect={setNewValue}
              creatable
              loading={isLoading}
              placeholder="New name"
              searchPlaceholder="Type a new name or pick an existing one..."
              emptyText="No existing values."
            />
            <p className="text-xs text-muted-foreground">
              Type a new name, or pick an existing value to merge into it.
            </p>
          </div>
          <DialogFooter>
            <Button type="button" variant="secondary" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={add.isPending}>
              {add.isPending ? "Renaming..." : "Rename"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
