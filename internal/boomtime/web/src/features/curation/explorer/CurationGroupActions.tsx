import { useState } from "react";
import { Eye, EyeOff, Pencil, Plus, SquareStack } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dropdown-menu";
import { axisLabel } from "@shared/lib/axes";
import { RenameGroupDialog } from "@boomtime/features/curation/explorer/RenameGroupDialog";
import { cn } from "@shared/lib/utils";
import type { GroupNode } from "@shared/features/explorer/explorerModel";
import type { SuppressInfo } from "@boomtime/features/curation/explorer/useSuppression";
import type { SpaceOption } from "@boomtime/features/curation/explorer/useSpaceMembership";

interface Props {
  node: GroupNode;
  suppress: SuppressInfo;
  isSuppressed: boolean;
  suppressBusy: boolean;
  onToggleSuppress: () => void;
  renamable: boolean;
  canAddToSpace: boolean;
  addableSpaces: SpaceOption[];
  spaceOptions: SpaceOption[];
  spaceBusy: boolean;
  onAddToSpace: (spaceId: number, spaceName: string) => void;
}

/**
 * Trailing curation actions for a group row: suppress toggle, rename (opens a
 * local dialog), add-to-Space. Rendered into the explorer's neutral group-action
 * slot by the curation layer, so the generic GroupRow carries no curation
 * knowledge and works on any domain's (axis, value) group nodes.
 */
export function CurationGroupActions({
  node: n,
  suppress,
  isSuppressed,
  suppressBusy,
  onToggleSuppress,
  renamable,
  canAddToSpace,
  addableSpaces,
  spaceOptions,
  spaceBusy,
  onAddToSpace,
}: Props) {
  const [renameOpen, setRenameOpen] = useState(false);

  return (
    <>
      {suppress.suppressible && (
        <button
          className={cn(
            "rounded p-1 transition-opacity hover:bg-background hover:text-foreground disabled:opacity-40",
            // The active-suppressed toggle stays visible; the "suppress"
            // action reveals on hover/focus like the pencil.
            isSuppressed
              ? "text-amber-500 opacity-100"
              : "text-muted-foreground opacity-0 focus:opacity-100 group-hover/row:opacity-100",
          )}
          title={
            isSuppressed
              ? `Unsuppress "${n.value}"`
              : "Suppress (hide from dashboards)"
          }
          disabled={suppressBusy}
          onClick={(e) => {
            e.stopPropagation();
            onToggleSuppress();
          }}
        >
          {isSuppressed ? (
            <Eye className="h-3.5 w-3.5" />
          ) : (
            <EyeOff className="h-3.5 w-3.5" />
          )}
        </button>
      )}
      {renamable && (
        <button
          className="rounded p-1 text-muted-foreground opacity-0 transition-opacity hover:bg-background hover:text-foreground focus:opacity-100 group-hover/row:opacity-100"
          title={`Rename ${axisLabel(n.axis)} "${n.value}"`}
          onClick={(e) => {
            e.stopPropagation();
            setRenameOpen(true);
          }}
        >
          <Pencil className="h-3.5 w-3.5" />
        </button>
      )}
      {canAddToSpace && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              className="rounded p-1 text-muted-foreground opacity-0 transition-opacity hover:bg-background hover:text-foreground focus:opacity-100 group-hover/row:opacity-100 data-[state=open]:opacity-100 disabled:opacity-40"
              title={`Add ${axisLabel(n.axis)} "${n.value}" to a Space`}
              disabled={spaceBusy}
              onClick={(e) => e.stopPropagation()}
            >
              <SquareStack className="h-3.5 w-3.5" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" onClick={(e) => e.stopPropagation()}>
            <DropdownMenuLabel className="text-xs">
              Add to Space
            </DropdownMenuLabel>
            {addableSpaces.length === 0 ? (
              <DropdownMenuItem disabled>
                {spaceOptions.length === 0
                  ? "No Spaces yet"
                  : "Already in every Space"}
              </DropdownMenuItem>
            ) : (
              addableSpaces.map((s) => (
                <DropdownMenuItem
                  key={s.id}
                  onSelect={() => onAddToSpace(s.id, s.name)}
                >
                  <Plus className="mr-2 h-3.5 w-3.5" />
                  {s.name}
                </DropdownMenuItem>
              ))
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      )}
      <RenameGroupDialog
        node={renameOpen ? n : null}
        onClose={() => setRenameOpen(false)}
      />
    </>
  );
}
