import { useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { DatabaseBackup, Download, Upload } from "lucide-react";
import { toast } from "sonner";
import { ApiError, api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader } from "@/components/ui/card";
import { RestoreConfirmDialog } from "@/features/heartbeats/RestoreConfirmDialog";

/**
 * Save/Load the ENTIRE database to/from a file. Save streams a full logical
 * dump (every table, every user, all settings — treat the file as sensitive);
 * Load uploads such a dump and replaces the whole app state after a typed
 * confirmation (RestoreConfirmDialog).
 */
export function BackupPanel() {
  const fileRef = useRef<HTMLInputElement | null>(null);
  const [pendingFile, setPendingFile] = useState<File | null>(null);

  const save = useMutation({
    mutationFn: api.exportDb,
    onSuccess: (blob) => {
      const stamp = new Date()
        .toISOString()
        .slice(0, 19)
        .replaceAll(":", "")
        .replace("T", "-");
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `boomtime-backup-${stamp}.zip`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    },
    onError: (e) =>
      toast.error(
        e instanceof ApiError ? `Backup failed: ${e.message}` : "Backup failed",
      ),
  });

  return (
    <Card className="rounded-lg p-4">
      <CardHeader className="flex-row flex-wrap items-center justify-between gap-3 space-y-0 p-0">
        <div className="flex items-center gap-2">
          <DatabaseBackup className="h-4 w-4 text-muted-foreground" />
          <span className="text-sm font-medium">Database backup</span>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => save.mutate()}
            disabled={save.isPending}
            title="Download a full backup of the entire database (all users, heartbeats, settings, tokens)"
          >
            <Download className={`h-4 w-4 ${save.isPending ? "animate-pulse" : ""}`} />
            {save.isPending ? "Preparing…" : "Save DB"}
          </Button>
          <Button
            variant="outline"
            size="sm"
            className="border-destructive/40 text-destructive hover:bg-destructive/10 hover:text-destructive"
            onClick={() => fileRef.current?.click()}
            title="Upload a backup and REPLACE the entire database with it"
          >
            <Upload className="h-4 w-4" />
            Load DB
          </Button>
          <input
            ref={fileRef}
            type="file"
            accept=".zip,application/zip"
            className="hidden"
            onChange={(e) => {
              const f = e.target.files?.[0] ?? null;
              // Allow re-selecting the same file next time.
              e.target.value = "";
              if (f) setPendingFile(f);
            }}
          />
        </div>
      </CardHeader>
      <CardContent className="p-0">
        <p className="mt-2 text-xs text-muted-foreground">
          Save downloads a complete snapshot of every table (the file contains
          password hashes and API tokens — store it securely). Load restores
          such a snapshot, <b>erasing all current data first</b>.
        </p>
      </CardContent>

      <RestoreConfirmDialog file={pendingFile} onClose={() => setPendingFile(null)} />
    </Card>
  );
}
