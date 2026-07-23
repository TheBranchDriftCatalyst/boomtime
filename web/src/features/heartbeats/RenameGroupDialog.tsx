import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dialog";
import { RemappingForm } from "@/features/curation/RemappingForm";
import { axisLabel } from "@/lib/axes";
import type { GroupNode } from "@/features/heartbeats/explorerModel";

interface RenameGroupDialogProps {
  /** The group being renamed; null = dialog closed. */
  node: GroupNode | null;
  onClose: () => void;
}

export function RenameGroupDialog({ node, onClose }: RenameGroupDialogProps) {
  return (
    <Dialog open={node !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        {node !== null && (
          <>
            <DialogHeader>
              <DialogTitle>Rename {axisLabel(node.axis).toLowerCase()}</DialogTitle>
              <DialogDescription>
                Remaps this value in your dashboards (merging into the target if
                it already exists). This is reversible — raw records are
                preserved; remove it anytime under Settings → Name remappings.
              </DialogDescription>
            </DialogHeader>
            {/* Shared form: axis is locked to this group's axis, pattern
                pre-filled with the clicked value (user can still switch to
                regex + edit). */}
            <RemappingForm
              presetAxis={node.axis}
              presetValue={node.value ?? ""}
              onDone={onClose}
              onCancel={onClose}
              layout="stacked"
              submitLabel="Rename"
            />
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
