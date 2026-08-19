import { useCallback } from "react";
import { Link } from "react-router";
import { SquareStack } from "lucide-react";
import {
  Badge,
  badgeVariants,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/badge";
import { cn } from "@shared/lib/utils";
import { CurationGroupActions } from "@boomtime/features/curation/explorer/CurationGroupActions";
import { useSuppression } from "@boomtime/features/curation/explorer/useSuppression";
import { useSpaceMembership } from "@boomtime/features/curation/explorer/useSpaceMembership";
import type { GroupAction } from "@shared/features/explorer/types";
import type { GroupNode } from "@shared/features/explorer/explorerModel";

/**
 * The curation layer — a fully self-contained, pluggable abstraction that
 * COMPOSES INTO the generic <GroupableExplorer> through its neutral group-action
 * slot. Nothing in features/explorer/* knows this exists; a domain opts in by
 * setting `useGroupDecorator: curationLayer()` on its DomainConfig, and drops
 * ALL curation code simply by not composing it.
 *
 * `curationLayer(options)` is a factory returning the `() => GroupAction` hook
 * the config expects. The produced decorator renders, per (axis, value) group
 * node: the suppressed/remapped/Space badges + dimming (as a GroupDecoration)
 * and the suppress/rename/add-to-Space actions (as CurationGroupActions). It
 * operates on any domain's group nodes — nothing here is heartbeats-specific.
 *
 * Call the factory ONCE (module scope) so the returned hook has a stable
 * identity across renders.
 */
export interface CurationLayerOptions {
  /** Offer the suppress (hide-from-dashboards) toggle + "Hidden" badge. */
  suppress?: boolean;
  /** Offer the rename action + "→ remapped" badge. */
  rename?: boolean;
  /** Offer the add-to-Space action + Space membership badges. */
  spaces?: boolean;
}

export function curationLayer(
  options: CurationLayerOptions = {},
): () => GroupAction {
  const suppressOn = options.suppress ?? true;
  const renameOn = options.rename ?? true;
  const spacesOn = options.spaces ?? true;

  return function useCurationDecorator(): GroupAction {
    const { getSuppressInfo, getRenamedTo, toggleSuppress, suppressBusy } =
      useSuppression();
    const { spaceOptions, getSpacesFor, canAddToSpace, addToSpace, spaceBusy } =
      useSpaceMembership();

    return useCallback(
      (node: GroupNode) => {
        const suppress = suppressOn
          ? getSuppressInfo(node)
          : { suppressible: false, ruleId: null };
        const isSuppressed = suppress.ruleId != null;
        const renamedTo = renameOn ? getRenamedTo(node) : null;
        const memberships = spacesOn ? getSpacesFor(node) : [];
        const memberSpaceIds = new Set(memberships.map((m) => m.spaceId));
        const canAdd = spacesOn && canAddToSpace(node);
        const addable = canAdd
          ? spaceOptions.filter((s) => !memberSpaceIds.has(s.id))
          : [];
        // Match the previous rule: non-null, and axis is not a synthetic/path
        // axis (day/entity have no meaningful rename target).
        const renamable =
          renameOn &&
          node.value != null &&
          node.axis !== "day" &&
          node.axis !== "entity";

        const badges = (
          <>
            {isSuppressed && (
              <Badge
                variant="outline"
                className="shrink-0 border-amber-500/40 text-xs text-amber-500"
              >
                Hidden
              </Badge>
            )}
            {renamedTo != null && (
              <Badge
                variant="outline"
                className="shrink-0 border-violet-500/40 font-mono text-xs text-violet-400"
                title={`Remapped to "${renamedTo}" in your dashboards (reversible in Settings → Name remappings)`}
              >
                → {renamedTo}
              </Badge>
            )}
            {memberships.map((m) => (
              <Link
                key={m.spaceId}
                to={`/app/space/${m.spaceId}`}
                onClick={(e) => e.stopPropagation()}
                title={`In Space "${m.spaceName}" — open it`}
                className={cn(
                  badgeVariants({ variant: "outline" }),
                  "shrink-0 border-sky-500/40 text-sky-400 hover:bg-sky-500/10",
                )}
              >
                <SquareStack className="mr-1 h-3 w-3" />
                {m.spaceName}
              </Link>
            ))}
          </>
        );

        const actions = (
          <CurationGroupActions
            node={node}
            suppress={suppress}
            isSuppressed={isSuppressed}
            suppressBusy={suppressBusy}
            onToggleSuppress={() => toggleSuppress(node, suppress)}
            renamable={renamable}
            canAddToSpace={canAdd}
            addableSpaces={addable}
            spaceOptions={spacesOn ? spaceOptions : []}
            spaceBusy={spaceBusy}
            onAddToSpace={(id, name) => addToSpace(node, id, name)}
          />
        );

        return { dimmed: isSuppressed, badges, actions };
      },
      [
        getSuppressInfo,
        getRenamedTo,
        getSpacesFor,
        canAddToSpace,
        spaceOptions,
        toggleSuppress,
        suppressBusy,
        addToSpace,
        spaceBusy,
      ],
    );
  };
}
