import { useEffect, useState, type KeyboardEvent } from "react";
import { X } from "lucide-react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";

interface SetTagsModalProps {
  project: string | null;
  initialTags: string[];
  onClose: () => void;
}

/** Tag editor for a project (replaces the old Tagify-based modal). */
export function SetTagsModal({
  project,
  initialTags,
  onClose,
}: SetTagsModalProps) {
  const [tags, setTags] = useState<string[]>(initialTags);
  const [input, setInput] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    setTags(initialTags);
  }, [initialTags, project]);

  function addTag() {
    const v = input.trim();
    if (v && !tags.includes(v)) setTags([...tags, v]);
    setInput("");
  }

  function onKeyDown(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === "Enter" || e.key === ",") {
      e.preventDefault();
      addTag();
    } else if (e.key === "Backspace" && !input && tags.length) {
      setTags(tags.slice(0, -1));
    }
  }

  async function save() {
    if (!project) return;
    setSaving(true);
    try {
      await api.setProjectTags(project, tags);
      toast.success("Tags updated");
      onClose();
    } catch {
      toast.error("Failed to update tags");
    } finally {
      setSaving(false);
    }
  }

  return (
    <Dialog open={project !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Set tags for {project}</DialogTitle>
        </DialogHeader>
        <div className="space-y-2">
          <Label>Tags</Label>
          <div className="flex flex-wrap gap-2 rounded-md border p-2">
            {tags.map((t) => (
              <Badge key={t} variant="secondary" className="gap-1">
                {t}
                <button onClick={() => setTags(tags.filter((x) => x !== t))}>
                  <X className="h-3 w-3" />
                </button>
              </Badge>
            ))}
            <Input
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={onKeyDown}
              onBlur={addTag}
              placeholder="Add a tag..."
              className="h-7 w-32 flex-1 border-0 shadow-none focus-visible:ring-0"
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="secondary" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={save} disabled={saving}>
            {saving ? "Saving..." : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
