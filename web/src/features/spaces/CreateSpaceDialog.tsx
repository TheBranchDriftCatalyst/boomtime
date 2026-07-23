import { useState } from "react";
import { useNavigate } from "react-router";
import { toast } from "sonner";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dialog";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Label } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";
import { useSpaceMutations } from "@/features/spaces/useSpaces";

interface CreateSpaceDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

/** "New space" dialog: names + creates a Space, then navigates to it. */
export function CreateSpaceDialog({ open, onOpenChange }: CreateSpaceDialogProps) {
  const navigate = useNavigate();
  const { create: createSpace } = useSpaceMutations();
  const [spaceName, setSpaceName] = useState("");

  function submitCreateSpace(e: React.FormEvent) {
    e.preventDefault();
    const name = spaceName.trim();
    if (!name) return;
    createSpace.mutate(name, {
      onSuccess: (space) => {
        onOpenChange(false);
        setSpaceName("");
        navigate(`/app/space/${space.id}`);
      },
      onError: () => toast.error("Failed to create space"),
    });
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        onOpenChange(o);
        if (!o) setSpaceName("");
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New space</DialogTitle>
        </DialogHeader>
        <form onSubmit={submitCreateSpace} className="space-y-4">
          <div className="space-y-1">
            <Label htmlFor="space-name">Name</Label>
            <Input
              id="space-name"
              value={spaceName}
              onChange={(e) => setSpaceName(e.target.value)}
              placeholder="Work"
              autoFocus
            />
          </div>
          <DialogFooter>
            <Button
              type="button"
              variant="secondary"
              onClick={() => onOpenChange(false)}
            >
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={createSpace.isPending || spaceName.trim() === ""}
            >
              {createSpace.isPending ? "Creating..." : "Create"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
