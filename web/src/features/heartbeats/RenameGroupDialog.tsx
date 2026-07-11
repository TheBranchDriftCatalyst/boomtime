import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { RemappingForm } from "@/components/curation/RemappingForm";
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
  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Rename {axisLabel(axis).toLowerCase()}</DialogTitle>
          <DialogDescription>
            Remaps this value in your dashboards (merging into the target if it
            already exists). This is reversible — raw records are preserved;
            remove it anytime under Settings → Name remappings.
          </DialogDescription>
        </DialogHeader>
        {/* Shared form: axis is locked to this group's axis, pattern pre-filled
            with the clicked value (user can still switch to regex + edit). */}
        <RemappingForm
          presetAxis={axis}
          presetValue={value}
          onDone={onClose}
          onCancel={onClose}
          layout="stacked"
          submitLabel="Rename"
        />
      </DialogContent>
    </Dialog>
  );
}
