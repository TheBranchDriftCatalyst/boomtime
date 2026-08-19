import { useMemo, useState } from "react";
import type { DragEvent } from "react";
import { ChevronLeft, ChevronRight, GripVertical, Plus, X } from "lucide-react";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/popover";
import type { Axis } from "@shared/features/explorer/types";

interface GroupByBarProps {
  axes: Axis[];
  groupBy: string[];
  onChange: (next: string[]) => void;
}

/** Ordered add/remove/reorder chip bar for the nesting axes. */
export function GroupByBar({ axes, groupBy, onChange }: GroupByBarProps) {
  // Index of the chip currently being dragged, and the index its dragged over.
  const [dragIndex, setDragIndex] = useState<number | null>(null);
  const [overIndex, setOverIndex] = useState<number | null>(null);

  const labelById = useMemo(
    () => new Map(axes.map((a) => [a.id, a.label])),
    [axes],
  );
  const available = axes.filter((a) => !groupBy.includes(a.id));

  // Distinct axis sections in encounter order (axes without one bucket first).
  const sections = useMemo(() => {
    const seen = new Set<string>();
    const out: string[] = [];
    for (const a of available) {
      const s = a.section ?? "";
      if (!seen.has(s)) {
        seen.add(s);
        out.push(s);
      }
    }
    return out;
  }, [available]);

  // Move the axis at `from` to sit at position `to`, preserving the others'
  // relative order. Shared by the ‹ › arrows and by drag-and-drop.
  function reorder(from: number, to: number) {
    if (
      from === to ||
      from < 0 ||
      to < 0 ||
      from >= groupBy.length ||
      to >= groupBy.length
    ) {
      return;
    }
    const next = [...groupBy];
    const [moved] = next.splice(from, 1);
    next.splice(to, 0, moved);
    onChange(next);
  }

  function move(index: number, dir: -1 | 1) {
    reorder(index, index + dir);
  }

  function onDragStart(e: DragEvent<HTMLDivElement>, index: number) {
    setDragIndex(index);
    setOverIndex(index);
    e.dataTransfer.effectAllowed = "move";
    // Carry the source index so a drop can reorder even without React state.
    e.dataTransfer.setData("text/plain", String(index));
  }

  function onDragOver(e: DragEvent<HTMLDivElement>, index: number) {
    // Required so the element becomes a valid drop target.
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    if (index !== overIndex) setOverIndex(index);
  }

  function onDrop(e: DragEvent<HTMLDivElement>, index: number) {
    e.preventDefault();
    const raw = e.dataTransfer.getData("text/plain");
    const from = raw === "" ? dragIndex : Number(raw);
    if (from != null && !Number.isNaN(from)) reorder(from, index);
    setDragIndex(null);
    setOverIndex(null);
  }

  function onDragEnd() {
    setDragIndex(null);
    setOverIndex(null);
  }

  function remove(axis: string) {
    onChange(groupBy.filter((a) => a !== axis));
  }

  function add(axis: string) {
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
          draggable
          onDragStart={(e) => onDragStart(e, i)}
          onDragOver={(e) => onDragOver(e, i)}
          onDrop={(e) => onDrop(e, i)}
          onDragEnd={onDragEnd}
          data-testid={`groupby-chip-${i}`}
          data-drag-over={overIndex === i && dragIndex !== i ? "true" : undefined}
          className={[
            "flex cursor-grab items-center gap-0.5 rounded-md border bg-secondary py-0.5 pl-1 pr-0.5 text-sm transition-shadow active:cursor-grabbing",
            dragIndex === i ? "opacity-40" : "",
            overIndex === i && dragIndex !== i
              ? "ring-2 ring-primary ring-offset-1 ring-offset-background"
              : "",
          ]
            .filter(Boolean)
            .join(" ")}
        >
          <GripVertical
            className="h-3.5 w-3.5 shrink-0 text-muted-foreground"
            aria-hidden
          />
          <span className="mr-1 font-mono text-xs text-muted-foreground">
            {i + 1}
          </span>
          <span className="font-medium">{labelById.get(axis) ?? axis}</span>
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
            {sections.map((section) => {
              const items = available.filter((a) => (a.section ?? "") === section);
              if (items.length === 0) return null;
              return (
                <div key={section} className="mb-1 last:mb-0">
                  {section && (
                    <p className="px-2 py-1 text-xs font-semibold uppercase tracking-wide text-muted-foreground">
                      {section}
                    </p>
                  )}
                  {items.map((a) => (
                    <button
                      key={a.id}
                      className="w-full rounded-sm px-2 py-1.5 text-left text-sm hover:bg-accent"
                      onClick={() => add(a.id)}
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
