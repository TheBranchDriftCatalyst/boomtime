import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Clipboard, Trash2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { copyToClipboard, formatDate } from "@/lib/utils";
import type { StoredApiToken } from "@/types/api";

interface TokenListModalProps {
  open: boolean;
  onClose: () => void;
}

// The stored id is base64(uuid); decode it to hand the raw UUID to the client.
function decodeToken(id: string): string {
  try {
    return atob(id);
  } catch {
    return id;
  }
}

export function TokenListModal({ open, onClose }: TokenListModalProps) {
  const qc = useQueryClient();
  const { data: tokens = [] } = useQuery({
    queryKey: qk.tokens(),
    queryFn: () => api.getTokens(),
    enabled: open,
  });

  const rename = useMutation({
    mutationFn: (v: { tokenId: string; tokenName: string }) =>
      api.renameToken(v),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.tokens() }),
    onError: () => toast.error("Failed to update the token"),
  });

  const remove = useMutation({
    mutationFn: (id: string) => api.deleteToken(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.tokens() }),
    onError: () => toast.error("Failed to delete the token"),
  });

  const sorted = [...tokens].sort((a, b) => a.id.localeCompare(b.id));

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Active API tokens</DialogTitle>
        </DialogHeader>
        <div className="max-h-[360px] overflow-y-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>ID</TableHead>
                <TableHead>Name</TableHead>
                <TableHead>Last usage</TableHead>
                <TableHead className="text-right" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {sorted.length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={4}
                    className="text-center text-muted-foreground"
                  >
                    No tokens yet
                  </TableCell>
                </TableRow>
              ) : (
                sorted.map((t) => (
                  <TokenRow
                    key={t.id}
                    token={t}
                    onRename={(name) =>
                      rename.mutate({ tokenId: t.id, tokenName: name })
                    }
                    onDelete={() => remove.mutate(t.id)}
                  />
                ))
              )}
            </TableBody>
          </Table>
        </div>
        <DialogFooter>
          <Button variant="secondary" onClick={onClose}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function TokenRow({
  token,
  onRename,
  onDelete,
}: {
  token: StoredApiToken;
  onRename: (name: string) => void;
  onDelete: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(token.name ?? "");

  return (
    <TableRow>
      <TableCell className="font-mono text-xs">
        {token.id.substring(0, 6)}
      </TableCell>
      <TableCell>
        {editing ? (
          <Input
            autoFocus
            maxLength={42}
            value={name}
            onChange={(e) => setName(e.target.value)}
            onBlur={() => {
              setEditing(false);
              if (name && name !== (token.name ?? "")) onRename(name);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") e.currentTarget.blur();
            }}
            className="h-7"
          />
        ) : (
          <button
            className="rounded px-1 hover:bg-accent"
            onClick={() => setEditing(true)}
            title="Click to rename"
          >
            {token.name || "-"}
          </button>
        )}
      </TableCell>
      <TableCell className="text-muted-foreground">
        {token.lastUsage ? formatDate(token.lastUsage) : "Not used"}
      </TableCell>
      <TableCell className="text-right">
        <div className="flex justify-end gap-1">
          <Button
            variant="secondary"
            size="icon"
            className="h-8 w-8"
            title="Copy to clipboard"
            onClick={async () => {
              await copyToClipboard(decodeToken(token.id));
              toast.success("Token copied to clipboard");
            }}
          >
            <Clipboard className="h-4 w-4" />
          </Button>
          <Button
            variant="destructive"
            size="icon"
            className="h-8 w-8"
            title="Delete token"
            onClick={onDelete}
          >
            <Trash2 className="h-4 w-4" />
          </Button>
        </div>
      </TableCell>
    </TableRow>
  );
}
