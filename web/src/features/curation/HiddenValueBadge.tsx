import { X } from "lucide-react";
import { Badge } from "@thebranchdriftcatalyst/catalyst-ui/ui/badge";

/**
 * Shared "hidden value" chip: a secondary badge with an unhide (X) button.
 * Used by the Hidden projects and Hidden sources Settings cards.
 */
export function HiddenValueBadge({
  value,
  onRemove,
}: {
  value: string;
  onRemove: () => void;
}) {
  return (
    <Badge variant="secondary" className="gap-1 py-1 pl-2.5">
      {value}
      <button
        onClick={onRemove}
        title="Unhide"
        className="rounded-full p-0.5 hover:bg-background"
      >
        <X className="h-3 w-3" />
      </button>
    </Badge>
  );
}
