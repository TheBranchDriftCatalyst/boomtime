import { Copy } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { copyToClipboard } from "@/lib/utils";

interface CreateTokenModalProps {
  token: string | null;
  onClose: () => void;
}

export function CreateTokenModal({ token, onClose }: CreateTokenModalProps) {
  return (
    <Dialog open={token !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Your new API token</DialogTitle>
          <DialogDescription>
            Use this token to set up your Wakatime client. Point the client's
            upstream API URL at this Boomtime instance
            (<code>/api/v1</code>).
          </DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-2 rounded-md border bg-muted p-3">
          <code className="flex-1 select-all break-all text-sm">{token}</code>
          <Button
            variant="secondary"
            size="icon"
            onClick={async () => {
              if (token) {
                await copyToClipboard(token);
                toast.success("Token copied to clipboard");
              }
            }}
          >
            <Copy className="h-4 w-4" />
          </Button>
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
