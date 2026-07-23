import { ChevronLeft, ChevronRight, Plus, X } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/popover";
import { AXES, axisLabel } from "@/lib/axes";
import type { HeartbeatAxis } from "@/types/api";

interface GroupByBarProps {
  groupBy: HeartbeatAxis[];
  onChange: (next: HeartbeatAxis[]) => void;
}

/** Ordered add/remove/reorder chip bar for the nesting axes. */
export function GroupByBar({ groupBy, onChange }: GroupByBarProps) {
  const available = AXES.filter((a) => !groupBy.includes(a.axis));

  function move(index: number, dir: -1 | 1) {
    const target = index + dir;
    if (target < 0 || target >= groupBy.length) return;
    const next = [...groupBy];
    [next[index], next[target]] = [next[target], next[index]];
    onChange(next);
  }

  function remove(axis: HeartbeatAxis) {
    onChange(groupBy.filter((a) => a !== axis));
  }

  function add(axis: HeartbeatAxis) {
    onChange([...groupBy, axis]);
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
      <span className="text-sm font-medium text-muted-foreground">
        Group by:
      </span>

      {groupBy.map((axis, i) => (
        <div
          key={axis}
          className="flex items-center gap-0.5 rounded-md border bg-secondary py-0.5 pl-2 pr-0.5 text-sm"
        >
          <span className="mr-1 font-mono text-xs text-muted-foreground">
            {i + 1}
          </span>
          <span className="font-medium">{axisLabel(axis)}</span>
          <button
            className="ml-1 rounded p-0.5 hover:bg-background disabled:opacity-30"
            onClick={() => move(i, -1)}
            disabled={i === 0}
            title="Move left"
          >
            <ChevronLeft className="h-3.5 w-3.5" />
          </button>
          <button
            className="rounded p-0.5 hover:bg-background disabled:opacity-30"
            onClick={() => move(i, 1)}
            disabled={i === groupBy.length - 1}
            title="Move right"
          >
            <ChevronRight className="h-3.5 w-3.5" />
          </button>
          <button
            className="rounded p-0.5 hover:bg-destructive/15 hover:text-destructive"
            onClick={() => remove(axis)}
            title="Remove"
          >
            <X className="h-3.5 w-3.5" />
          </button>
        </div>
      ))}

      {available.length > 0 && (
        <Popover>
          <PopoverTrigger asChild>
            <Button variant="outline" size="sm">
              <Plus className="h-4 w-4" />
              Add axis
            </Button>
          </PopoverTrigger>
          <PopoverContent align="start" className="w-56 p-2">
            {(["General", "Source"] as const).map((section) => {
              const items = available.filter((a) => a.section === section);
              if (items.length === 0) return null;
              return (
                <div key={section} className="mb-1 last:mb-0">
                  <p className="px-2 py-1 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                    {section}
                  </p>
                  {items.map((a) => (
                    <button
                      key={a.axis}
                      className="w-full rounded-sm px-2 py-1.5 text-left text-sm hover:bg-accent"
                      onClick={() => add(a.axis)}
                    >
                      {a.label}
                    </button>
                  ))}
                </div>
              );
            })}
          </PopoverContent>
        </Popover>
      )}
    </div>
  );
}
