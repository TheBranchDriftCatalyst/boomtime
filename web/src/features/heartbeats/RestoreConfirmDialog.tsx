import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { ApiError, api } from "@/lib/api";
import { formatBytes } from "@/lib/utils";
import { useAuth } from "@/features/auth/useAuth";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

interface RestoreConfirmDialogProps {
  /** The selected backup archive; null = dialog closed. */
  file: File | null;
  onClose: () => void;
}

/** The phrase the user must type before the destructive restore arms. */
const CONFIRM_PHRASE = "REPLACE";

/**
 * Final guard before a whole-database restore: shows what was selected, spells
 * out exactly what gets erased, and requires typing REPLACE. On success every
 * cached query is dropped; if the restored token tables don't contain this
 * session (backup from another instance/time), the user is logged out.
 */
export function RestoreConfirmDialog({ file, onClose }: RestoreConfirmDialogProps) {
  const qc = useQueryClient();
  const { logout } = useAuth();
  const [typed, setTyped] = useState("");

  const restore = useMutation({
    mutationFn: api.importDb,
    onSuccess: async (s) => {
      toast.success(
        `Database restored — ${s.totalRows.toLocaleString()} rows across ${Object.keys(s.tables).length} tables`,
      );
      // Every cached payload predates the restore.
      qc.clear();
      setTyped("");
      onClose();
      // The restored auth/refresh tokens may not include this session; verify
      // and force a clean re-login when they don't.
      try {
        await api.currentUser();
      } catch (e) {
        if (e instanceof ApiError) {
          toast.info("Your session isn't part of the restored backup — please log in again.");
          await logout();
        }
      }
    },
    onError: (e) =>
      toast.error(
        e instanceof ApiError ? `Restore failed: ${e.message}` : "Restore failed",
      ),
  });

  const close = () => {
    if (restore.isPending) return; // don't dismiss mid-restore
    setTyped("");
    onClose();
  };
  const armed = typed === CONFIRM_PHRASE;

  return (
    <Dialog open={file !== null} onOpenChange={(o) => !o && close()}>
      <DialogContent>
        {file !== null && (
          <>
            <DialogHeader>
              <DialogTitle>Replace the entire database?</DialogTitle>
              <DialogDescription>
                Restoring{" "}
                <span className="font-medium text-foreground">
                  {file.name}
                </span>{" "}
                ({formatBytes(file.size)}) <b>ERASES every table</b> —
                heartbeats, projects, users, API tokens, settings, spaces, and
                import history — and replaces them with the backup&apos;s
                contents. This cannot be undone, and you will likely be logged
                out afterwards.
              </DialogDescription>
            </DialogHeader>

            <div className="space-y-2">
              <p className="text-sm text-muted-foreground">
                Type <span className="font-mono font-semibold">{CONFIRM_PHRASE}</span>{" "}
                to confirm:
              </p>
              <Input
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                placeholder={CONFIRM_PHRASE}
                disabled={restore.isPending}
                aria-label="Type REPLACE to confirm"
                autoComplete="off"
              />
            </div>

            <DialogFooter>
              <Button variant="outline" onClick={close} disabled={restore.isPending}>
                Cancel
              </Button>
              <Button
                variant="destructive"
                disabled={!armed || restore.isPending}
                onClick={() => restore.mutate(file)}
              >
                {restore.isPending ? "Restoring…" : "Erase & restore"}
              </Button>
            </DialogFooter>
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}
