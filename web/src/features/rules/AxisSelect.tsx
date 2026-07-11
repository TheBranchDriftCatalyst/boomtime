import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Label } from "@/components/ui/label";
import { axisLabel } from "@/lib/axes";
import { cn } from "@/lib/utils";
import type { HeartbeatAxis } from "@/types/api";

interface AxisSelectProps {
  /** Axes offered in the dropdown (e.g. CURATABLE_AXES or a subset). */
  axes: readonly HeartbeatAxis[];
  value: HeartbeatAxis;
  onChange: (axis: HeartbeatAxis) => void;
  /** Render the axis as static text instead of a dropdown (preset/edit use). */
  locked?: boolean;
  /** Field label above the control; pass null to render the control alone. */
  label?: string | null;
  /** Extra classes for the dropdown trigger (width variants). */
  triggerClassName?: string;
  size?: "sm" | "default";
}

/**
 * Shared "Axis" field: a small label plus a dropdown of axis choices (or a
 * locked static value). Used by RemappingForm, SpaceRuleForm, and the Settings
 * hidden-sources picker so there is exactly one axis-selector implementation.
 */
export function AxisSelect({
  axes,
  value,
  onChange,
  locked = false,
  label = "Axis",
  triggerClassName,
  size = "sm",
}: AxisSelectProps) {
  const control = locked ? (
    <div className="flex h-8 w-32 items-center rounded-md border bg-muted px-3 text-sm text-muted-foreground">
      {axisLabel(value)}
    </div>
  ) : (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          type="button"
          variant="outline"
          size={size}
          className={cn("justify-between", triggerClassName)}
        >
          {axisLabel(value)}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-72 overflow-y-auto">
        {axes.map((a) => (
          <DropdownMenuItem key={a} onSelect={() => onChange(a)}>
            {axisLabel(a)}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );

  if (label === null) return control;
  return (
    <div className="space-y-1">
      <Label className="text-xs">{label}</Label>
      {control}
    </div>
  );
}
