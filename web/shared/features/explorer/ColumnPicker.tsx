import { Columns3 } from "lucide-react";
import type { VisibilityState } from "@tanstack/react-table";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dropdown-menu";
import type { Column } from "@shared/features/explorer/types";

/** "Columns" dropdown toggling leaf-column visibility in the explorer table. */
export function ColumnPicker({
  columns,
  visibility,
  onToggle,
}: {
  columns: Column<unknown>[];
  visibility: VisibilityState;
  onToggle: (id: string, visible: boolean) => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm">
          <Columns3 className="h-4 w-4" />
          Columns
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuLabel>Leaf columns</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {columns.map((c) => (
          <DropdownMenuCheckboxItem
            key={c.id}
            checked={visibility[c.id] !== false}
            onCheckedChange={(v) => onToggle(c.id, v)}
            onSelect={(e) => e.preventDefault()}
          >
            {c.header}
          </DropdownMenuCheckboxItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
